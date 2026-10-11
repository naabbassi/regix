// Package inventory turns the registry's low-level API into what the UI shows: repositories with
// their sizes, tags with their build times, images with their layers. It also carries out
// deletions, with the safety rules that go with them.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"regix/internal/registry"
)

const (
	// loadTimeout bounds one listing of a repository or of the whole registry.
	loadTimeout = 2 * time.Minute
	// summaryCacheSize is how many inspected digests are remembered (a few hundred bytes each).
	summaryCacheSize = 20000
	maxDeleteBatch   = 1000
)

// ErrReadOnly is returned for destructive operations when they are switched off.
var ErrReadOnly = errors.New("this Regix instance is read-only")

// InvalidError reports a request that cannot be carried out as given.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return e.Reason }

// Identity is who a request is made for: the name to log, the credentials to use and the cache
// partition (users with different credentials may see different things).
type Identity struct {
	User        string
	Credentials registry.Credentials
	Scope       string
}

type Options struct {
	ReadOnly      bool
	ProtectedTags []string
	CacheTTL      time.Duration
}

type Service struct {
	registry  *registry.Client
	opts      Options
	log       *slog.Logger
	overviews *ttlCache[*Overview]
	tags      *ttlCache[[]Tag]
	// summaries are keyed by digest, whose content never changes, so they never expire.
	summaries *lruCache[summary]
}

// summary is what the tag list needs to know about a digest.
type summary struct {
	kind      string
	mediaType string
	size      int64
	created   *time.Time
	platforms []string
	blobs     []blob
}

func New(client *registry.Client, opts Options, log *slog.Logger) *Service {
	return &Service{
		registry:  client,
		opts:      opts,
		log:       log,
		overviews: newTTLCache[*Overview](opts.CacheTTL),
		tags:      newTTLCache[[]Tag](opts.CacheTTL),
		summaries: newLRUCache[summary](summaryCacheSize),
	}
}

// Overview lists every repository with its tag count, size and last build time.
func (s *Service) Overview(ctx context.Context, id Identity, fresh bool) (*Overview, error) {
	return s.overviews.get(ctx, id.Scope, fresh, func(ctx context.Context) (*Overview, error) {
		names, err := s.registry.Catalog(ctx, id.Credentials)
		if err != nil {
			return nil, err
		}
		sort.Strings(names)

		perRepo := make([][]Tag, len(names))
		overview := &Overview{Repositories: make([]RepositorySummary, len(names)), GeneratedAt: time.Now().UTC()}
		var wait sync.WaitGroup
		for i, name := range names {
			wait.Add(1)
			go func() {
				defer wait.Done()
				repo := RepositorySummary{Name: name}
				if !registry.ValidRepository(name) {
					repo.Error = "invalid repository name"
				} else if tags, err := s.Tags(ctx, id, name, fresh); err != nil {
					repo.Error = explain(err)
				} else {
					perRepo[i] = tags
					repo.Tags = len(tags)
					repo.Images, repo.Size, repo.LastUpdated = measure(tags)
				}
				overview.Repositories[i] = repo
			}()
		}
		wait.Wait()

		var all []Tag
		for _, tags := range perRepo {
			all = append(all, tags...)
		}
		overview.Totals.Repositories = len(names)
		overview.Totals.Tags = len(all)
		overview.Totals.Images, overview.Totals.Size, _ = measure(all)
		return overview, nil
	})
}

// measure counts the distinct images and the bytes of the distinct blobs behind a set of tags,
// and finds the newest build time.
func measure(tags []Tag) (images int, size int64, newest *time.Time) {
	digests, blobs := map[string]bool{}, map[string]bool{}
	for _, tag := range tags {
		if tag.Digest != "" {
			digests[tag.Digest] = true
		}
		for _, b := range tag.blobs {
			if !blobs[b.digest] {
				blobs[b.digest] = true
				size += b.size
			}
		}
		if tag.Created != nil && (newest == nil || tag.Created.After(*newest)) {
			newest = tag.Created
		}
	}
	return len(digests), size, newest
}

// Tags lists the tags of a repository, newest image first. The result is shared between
// callers and must not be modified.
func (s *Service) Tags(ctx context.Context, id Identity, repo string, fresh bool) ([]Tag, error) {
	if !registry.ValidRepository(repo) {
		return nil, &InvalidError{"invalid repository name"}
	}
	return s.tags.get(ctx, id.Scope+"|"+repo, fresh, func(ctx context.Context) ([]Tag, error) {
		names, err := s.registry.Tags(ctx, id.Credentials, repo)
		if err != nil {
			return nil, err
		}
		tags := make([]Tag, len(names))
		rejected := make([]error, len(names))
		var wait sync.WaitGroup
		for i, name := range names {
			wait.Add(1)
			go func() {
				defer wait.Done()
				tags[i], rejected[i] = s.describeTag(ctx, id, repo, name)
			}()
		}
		wait.Wait()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Rejected credentials are not a property of one tag; they fail the whole listing.
		if err := errors.Join(rejected...); err != nil {
			return nil, err
		}
		sort.SliceStable(tags, func(a, b int) bool {
			ca, cb := tags[a].Created, tags[b].Created
			switch {
			case ca != nil && cb != nil && !ca.Equal(*cb):
				return ca.After(*cb)
			case (ca == nil) != (cb == nil):
				return ca != nil
			}
			return tags[a].Name < tags[b].Name
		})
		return tags, nil
	})
}

// describeTag resolves and summarises one tag. A problem with the tag is noted on the tag; the
// returned error is set only when the registry rejected the credentials.
func (s *Service) describeTag(ctx context.Context, id Identity, repo, name string) (Tag, error) {
	tag := Tag{Name: name, Protected: s.protected(repo, name)}
	failed := func(err error) (Tag, error) {
		tag.Error = explain(err)
		if registry.StatusOf(err) == http.StatusUnauthorized {
			return tag, err
		}
		return tag, nil
	}
	if !registry.ValidTag(name) {
		tag.Error = "invalid tag name"
		return tag, nil
	}
	digest, err := s.registry.Digest(ctx, id.Credentials, repo, name)
	if err != nil {
		return failed(err)
	}
	tag.Digest = digest

	key := id.Scope + "|" + repo + "|" + digest
	sum, ok := s.summaries.get(key)
	if !ok {
		image, err := s.inspect(ctx, id, repo, digest)
		if err != nil {
			return failed(err)
		}
		sum = summarize(image)
		if complete(image) {
			s.summaries.put(key, sum)
		}
	}
	tag.Kind, tag.MediaType, tag.Size, tag.Created = sum.kind, sum.mediaType, sum.size, sum.created
	tag.Platforms, tag.blobs = sum.platforms, sum.blobs
	return tag, nil
}

func summarize(image *Image) summary {
	sum := summary{kind: image.Kind, mediaType: image.MediaType, size: image.Size, created: image.Created}
	seen := map[string]bool{}
	for _, platform := range image.Platforms {
		if !platform.Attestation && platform.Platform != "" {
			sum.platforms = append(sum.platforms, platform.Platform)
		}
		for _, b := range platform.blobs {
			if !seen[b.digest] {
				seen[b.digest] = true
				sum.blobs = append(sum.blobs, b)
			}
		}
	}
	return sum
}

// complete reports whether every part of the image could be read. A partial result may be due
// to a passing error and is not remembered.
func complete(image *Image) bool {
	for _, platform := range image.Platforms {
		if platform.Error != "" {
			return false
		}
	}
	return true
}

// Image describes one manifest, addressed by tag or digest.
func (s *Service) Image(ctx context.Context, id Identity, repo, reference string) (*Image, error) {
	if !registry.ValidRepository(repo) {
		return nil, &InvalidError{"invalid repository name"}
	}
	if !registry.ValidTag(reference) && !registry.ValidDigest(reference) {
		return nil, &InvalidError{"invalid tag or digest"}
	}
	image, err := s.inspect(ctx, id, repo, reference)
	if err != nil {
		return nil, err
	}
	image.Tags = []Tag{}
	// The tag list is context, not the answer: the image is still worth showing without it.
	if tags, err := s.Tags(ctx, id, repo, false); err == nil {
		for _, tag := range tags {
			if tag.Digest == image.Digest {
				image.Tags = append(image.Tags, tag)
			}
		}
	}
	return image, nil
}

// Delete removes tags. An image whose every tag is removed is deleted from the registry; an
// image that keeps other tags only loses the ones named. A tag is skipped when it is protected,
// gone, or no longer points at the digest the caller saw.
func (s *Service) Delete(ctx context.Context, id Identity, repo string, refs []TagRef) ([]DeleteResult, error) {
	if s.opts.ReadOnly {
		return nil, ErrReadOnly
	}
	if !registry.ValidRepository(repo) {
		return nil, &InvalidError{"invalid repository name"}
	}
	if len(refs) == 0 || len(refs) > maxDeleteBatch {
		return nil, &InvalidError{fmt.Sprintf("name between 1 and %d tags to delete", maxDeleteBatch)}
	}

	current, err := s.Tags(ctx, id, repo, true)
	if err != nil {
		return nil, err
	}
	digestOf, tagsOf := map[string]string{}, map[string][]string{}
	for _, tag := range current {
		if tag.Digest != "" {
			digestOf[tag.Name] = tag.Digest
			tagsOf[tag.Digest] = append(tagsOf[tag.Digest], tag.Name)
		}
	}

	results := make([]DeleteResult, len(refs))
	selected := map[string][]int{} // digest -> positions in results
	requested := map[string]bool{}
	for i, ref := range refs {
		results[i] = DeleteResult{Tag: ref.Name, Digest: ref.Digest, Action: ActionSkipped}
		digest, exists := digestOf[ref.Name]
		switch {
		case requested[ref.Name]:
			results[i].Error = "named twice"
		case !exists:
			results[i].Error = "tag no longer exists"
		case digest != ref.Digest:
			results[i].Error = "tag now points at a different image; reload and try again"
		case s.protected(repo, ref.Name):
			results[i].Error = "tag is protected"
		default:
			selected[digest] = append(selected[digest], i)
		}
		requested[ref.Name] = true
	}

	for digest, positions := range selected {
		if len(positions) == len(tagsOf[digest]) {
			err := s.registry.DeleteManifest(ctx, id.Credentials, repo, digest)
			for _, i := range positions {
				s.record(&results[i], id, repo, ActionImageDeleted, err)
			}
			continue
		}
		for _, i := range positions {
			err := s.registry.DeleteTag(ctx, id.Credentials, repo, results[i].Tag)
			s.record(&results[i], id, repo, ActionUntagged, err)
		}
	}

	s.tags.clear()
	s.overviews.clear()
	return results, nil
}

// record stores the outcome of one deletion and writes the audit log line for it.
func (s *Service) record(result *DeleteResult, id Identity, repo, action string, err error) {
	attrs := []any{"user", id.User, "repository", repo, "tag", result.Tag, "digest", result.Digest, "action", action}
	if err != nil {
		result.Error = explain(err)
		if registry.StatusOf(err) == http.StatusMethodNotAllowed {
			result.Error = "the registry does not allow this deletion (is storage.delete.enabled set?)"
		}
		s.log.Warn("delete failed", append(attrs, "error", err)...)
		return
	}
	result.Action = action
	s.log.Info("deleted", attrs...)
}

// protected reports whether a tag matches one of the configured protection patterns.
func (s *Service) protected(repo, tag string) bool {
	for _, pattern := range s.opts.ProtectedTags {
		subject := tag
		if strings.Contains(pattern, ":") {
			subject = repo + ":" + tag
		}
		if globMatch(pattern, subject) {
			return true
		}
	}
	return false
}

// globMatch matches text against a pattern in which `*` stands for any run of characters.
func globMatch(pattern, text string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == text
	}
	if !strings.HasPrefix(text, parts[0]) {
		return false
	}
	text = text[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(text, part)
		if i < 0 {
			return false
		}
		text = text[i+len(part):]
	}
	return strings.HasSuffix(text, last)
}

// explain turns an error into a short message that is safe to show. Network errors are
// summarised, because their text names internal hosts and addresses.
func explain(err error) string {
	var regErr *registry.Error
	if errors.As(err, &regErr) {
		switch regErr.Status {
		case http.StatusUnauthorized:
			return "the registry rejected the credentials"
		case http.StatusForbidden:
			return "the registry denied access"
		case http.StatusNotFound:
			return "not found in the registry"
		}
		return regErr.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "the registry took too long to answer"
	}
	var netErr *url.Error
	if errors.As(err, &netErr) {
		return "the registry could not be reached"
	}
	return err.Error()
}

// Explain is explain for other packages.
func Explain(err error) string { return explain(err) }
