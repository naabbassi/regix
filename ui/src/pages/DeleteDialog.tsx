import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { api, type DeleteResult, type Tag } from '../api';
import { Icon } from '../components';
import { plural } from '../lib';

interface Props {
  repository: string;
  /** The tags to delete. */
  tags: Tag[];
  /** Every tag of the repository, to tell whether an image keeps other tags. */
  allTags: Tag[];
  onClose: () => void;
  /** Called once the registry has been changed, with what happened to each tag. */
  onDeleted: (results: DeleteResult[]) => void;
}

/**
 * Confirms a deletion. It spells out the consequence for each tag before anything happens:
 * an image is deleted when none of its tags remain, otherwise only the tag is removed.
 */
export function DeleteDialog({ repository, tags, allTags, onClose, onDeleted }: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [typed, setTyped] = useState('');
  const [busy, setBusy] = useState(false);
  const [failures, setFailures] = useState<DeleteResult[]>();
  const [error, setError] = useState('');

  useEffect(() => dialog.current?.showModal(), []);

  const plan = useMemo(() => {
    const selected = new Set(tags.map((t) => t.name));
    return tags.map((tag) => ({
      tag,
      remaining: allTags.filter((other) => other.digest === tag.digest && !selected.has(other.name)).map((t) => t.name),
    }));
  }, [tags, allTags]);
  const images = new Set(plan.filter((p) => p.remaining.length === 0).map((p) => p.tag.digest)).size;

  // Typing the name back is the speed bump: a stray click cannot delete an image.
  const phrase = tags.length === 1 ? tags[0].name : repository;
  const confirmed = typed.trim() === phrase;

  const submit = (event: Event) => {
    event.preventDefault();
    if (!confirmed || busy) return;
    setBusy(true);
    setError('');
    api
      .remove(repository, tags.map((t) => ({ name: t.name, digest: t.digest! })))
      .then((results) => {
        const failed = results.filter((r) => r.action === 'skipped');
        onDeleted(results);
        if (failed.length === 0) onClose();
        else setFailures(failed);
      })
      .catch((failure: Error) => setError(failure.message))
      .finally(() => setBusy(false));
  };

  return (
    <dialog ref={dialog} class="dialog" onClose={onClose} aria-labelledby="delete-title">
      {failures ? (
        <div class="dialog-body">
          <h2 id="delete-title">Some tags were not deleted</h2>
          <ul class="plan">
            {failures.map((failure) => (
              <li>
                <code>{failure.tag}</code>
                <span class="plan-note danger">{failure.error}</span>
              </li>
            ))}
          </ul>
          <div class="dialog-actions">
            <button type="button" class="button" onClick={onClose}>
              Close
            </button>
          </div>
        </div>
      ) : (
        <form class="dialog-body" onSubmit={submit}>
          <h2 id="delete-title">
            Delete {tags.length === 1 ? 'tag' : plural(tags.length, 'tag')} from <code>{repository}</code>?
          </h2>
          <ul class="plan">
            {plan.map(({ tag, remaining }) => (
              <li>
                <code>{tag.name}</code>
                {remaining.length === 0 ? (
                  <span class="plan-note danger">Image will be deleted</span>
                ) : (
                  <span class="plan-note">
                    Tag removed · image stays as <code>{remaining.slice(0, 3).join(', ')}</code>
                    {remaining.length > 3 && ` +${remaining.length - 3}`}
                  </span>
                )}
              </li>
            ))}
          </ul>
          <p class="notice">
            <Icon name="alert" />
            <span>
              {images > 0
                ? `This permanently deletes ${plural(images, 'image')}. Anything still running ${images === 1 ? 'it' : 'them'} will fail to pull it again. `
                : 'No image is deleted; the tags can be pushed again. '}
              Disk space is freed when the registry next runs its garbage collection.
            </span>
          </p>
          <label class="field">
            <span>
              Type <code>{phrase}</code> to confirm
            </span>
            <input
              value={typed}
              autofocus
              autocomplete="off"
              autocapitalize="none"
              spellcheck={false}
              onInput={(event) => setTyped(event.currentTarget.value)}
            />
          </label>
          {error && (
            <div class="alert" role="alert">
              <Icon name="alert" />
              {error}
            </div>
          )}
          <div class="dialog-actions">
            <button type="button" class="button" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" class="button button-danger" disabled={!confirmed || busy}>
              {busy ? 'Deleting…' : 'Delete'}
            </button>
          </div>
        </form>
      )}
    </dialog>
  );
}

/** A one-line summary of a deletion, for the toast. */
export function describeDeletion(results: DeleteResult[]): string {
  const deleted = results.filter((r) => r.action !== 'skipped');
  const images = new Set(deleted.filter((r) => r.action === 'image-deleted').map((r) => r.digest)).size;
  if (deleted.length === 0) return 'Nothing was deleted';
  return `Removed ${plural(deleted.length, 'tag')}` + (images > 0 ? ` and deleted ${plural(images, 'image')}` : '');
}
