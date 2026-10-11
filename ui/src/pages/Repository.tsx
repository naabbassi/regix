import { useMemo, useState } from 'preact/hooks';
import { api, type DeleteResult, type Session, type Tag } from '../api';
import {
  Breadcrumbs,
  Command,
  CopyButton,
  ErrorMessage,
  Icon,
  Message,
  PlatformChips,
  RefreshButton,
  SearchBox,
  Select,
  SkeletonRows,
  toast,
} from '../components';
import { formatAge, formatBytes, formatDate, imagePath, Link, plural, shortDigest, useLoad } from '../lib';
import { DeleteDialog, describeDeletion } from './DeleteDialog';

type Sort = 'newest' | 'oldest' | 'name' | 'size';

const sorters: Record<Sort, (a: Tag, b: Tag) => number> = {
  newest: (a, b) => (b.created ?? '').localeCompare(a.created ?? '') || a.name.localeCompare(b.name),
  oldest: (a, b) => (a.created ?? '9').localeCompare(b.created ?? '9') || a.name.localeCompare(b.name),
  name: (a, b) => a.name.localeCompare(b.name, 'en', { numeric: true }),
  size: (a, b) => b.size - a.size,
};

export function Repository({ repository, session }: { repository: string; session: Session }) {
  const tags = useLoad(repository, (fresh) => api.tags(repository, fresh));
  const [search, setSearch] = useState('');
  const [sort, setSort] = useState<Sort>('newest');
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [deleting, setDeleting] = useState<Tag[]>();

  const all = tags.data ?? [];
  const visible = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return all.filter((t) => t.name.toLowerCase().includes(needle) || t.digest?.includes(needle)).sort(sorters[sort]);
  }, [all, search, sort]);

  const canDelete = (tag: Tag) => !session.readOnly && !tag.protected && !!tag.digest;
  const selectable = visible.filter(canDelete);
  const chosen = all.filter((t) => selected.has(t.name));
  const allChosen = selectable.length > 0 && selectable.every((t) => selected.has(t.name));

  const toggle = (name: string) => {
    const next = new Set(selected);
    if (!next.delete(name)) next.add(name);
    setSelected(next);
  };
  const onDeleted = (results: DeleteResult[]) => {
    const gone = new Set(results.filter((r) => r.action !== 'skipped').map((r) => r.tag));
    toast(describeDeletion(results), gone.size > 0 ? 'success' : 'error');
    tags.set(all.filter((t) => !gone.has(t.name)));
    setSelected(new Set());
    tags.reload(true);
  };

  const pullHost = session.pullHost;
  const stats = useMemo(() => ({ images: new Set(all.map((t) => t.digest)).size }), [all]);

  return (
    <>
      <Breadcrumbs items={[{ label: 'Repositories', href: '/' }, { label: repository }]} />
      <div class="page-head">
        <div>
          <h1 class="mono-title">{repository}</h1>
          <p class="muted">{tags.data ? `${plural(all.length, 'tag')} · ${plural(stats.images, 'image')}` : 'Loading…'}</p>
        </div>
        <RefreshButton loading={tags.loading} onClick={() => tags.reload(true)} />
      </div>
      <Command text={`docker pull ${pullHost}/${repository}`} />

      <div class="toolbar">
        <SearchBox value={search} onInput={setSearch} placeholder="Filter tags or digests" />
        <span class="spacer" />
        {chosen.length > 0 && (
          <>
            <button type="button" class="button" onClick={() => setSelected(new Set())}>
              Clear
            </button>
            <button type="button" class="button button-danger" onClick={() => setDeleting(chosen)}>
              <Icon name="trash" />
              Delete {chosen.length}
            </button>
          </>
        )}
        <Select
          label="Sort"
          value={sort}
          onChange={setSort}
          options={[
            ['newest', 'Newest'],
            ['oldest', 'Oldest'],
            ['name', 'Name'],
            ['size', 'Size'],
          ]}
        />
      </div>

      {tags.error && !tags.data ? (
        <ErrorMessage error={tags.error} retry={() => tags.reload()} />
      ) : !tags.data ? (
        <div class="card">
          <SkeletonRows />
        </div>
      ) : visible.length === 0 ? (
        <div class="card">
          {search ? (
            <Message icon="search" title="No matching tags">
              Nothing matches “{search}”.
            </Message>
          ) : (
            <Message icon="inbox" title="No tags">
              This repository has no images. Its name stays in the registry until the registry’s storage is cleaned up.
            </Message>
          )}
        </div>
      ) : (
        <div class={`card table tags ${session.readOnly ? 'no-select' : ''}`} role="table">
          <div class="row row-head" role="row">
            {!session.readOnly && (
              <span role="columnheader">
                <input
                  type="checkbox"
                  aria-label="Select all tags"
                  checked={allChosen}
                  disabled={selectable.length === 0}
                  onChange={() => setSelected(allChosen ? new Set() : new Set(selectable.map((t) => t.name)))}
                />
              </span>
            )}
            <span role="columnheader">Tag</span>
            <span role="columnheader" class="col-digest">Digest</span>
            <span role="columnheader" class="col-platform">Platform</span>
            <span role="columnheader" class="num">Size</span>
            <span role="columnheader" class="col-date">Built</span>
            <span role="columnheader" />
          </div>
          {visible.map((tag) => (
            <div class={`row ${selected.has(tag.name) ? 'is-selected' : ''}`} role="row" key={tag.name}>
              {!session.readOnly && (
                <span role="cell">
                  <input
                    type="checkbox"
                    aria-label={`Select ${tag.name}`}
                    checked={selected.has(tag.name)}
                    disabled={!canDelete(tag)}
                    title={tag.protected ? 'This tag is protected' : undefined}
                    onChange={() => toggle(tag.name)}
                  />
                </span>
              )}
              <span role="cell" class="cell-name">
                <Link href={imagePath(repository, tag.name)} class="tag-name">
                  {tag.name}
                </Link>
                {tag.protected && (
                  <span class="badge" title="Protected by this Regix’s configuration; it cannot be deleted here">
                    <Icon name="lock" size={12} />
                    <span class="hide-narrow">Protected</span>
                  </span>
                )}
                {tag.kind === 'artifact' && <span class="badge">Artifact</span>}
                {tag.error && <span class="badge badge-danger" title={tag.error}>Unreadable</span>}
              </span>
              <span role="cell" class="col-digest digest">
                {tag.digest && (
                  <>
                    <code title={tag.digest}>{shortDigest(tag.digest)}</code>
                    <CopyButton text={tag.digest} label="Copy digest" />
                  </>
                )}
              </span>
              <span role="cell" class="col-platform">
                <PlatformChips platforms={tag.platforms} />
              </span>
              <span role="cell" class="num">{tag.digest ? formatBytes(tag.size) : '—'}</span>
              <span role="cell" class="col-date muted" title={formatDate(tag.created)}>
                {formatAge(tag.created)}
              </span>
              <span role="cell" class="actions">
                <CopyButton text={`docker pull ${pullHost}/${repository}:${tag.name}`} label="Copy pull command" />
                {canDelete(tag) && (
                  <button type="button" class="icon-button danger" title="Delete tag" aria-label={`Delete ${tag.name}`} onClick={() => setDeleting([tag])}>
                    <Icon name="trash" />
                  </button>
                )}
              </span>
            </div>
          ))}
        </div>
      )}

      {deleting && (
        <DeleteDialog repository={repository} tags={deleting} allTags={all} onClose={() => setDeleting(undefined)} onDeleted={onDeleted} />
      )}
    </>
  );
}
