import { useMemo, useState } from 'preact/hooks';
import { api, type RepositorySummary } from '../api';
import { ErrorMessage, Icon, Message, RefreshButton, SearchBox, Select, SkeletonRows } from '../components';
import { formatAge, formatBytes, formatDate, Link, plural, repositoryPath, useLoad } from '../lib';

type Sort = 'name' | 'updated' | 'size' | 'tags';

const sorters: Record<Sort, (a: RepositorySummary, b: RepositorySummary) => number> = {
  name: (a, b) => a.name.localeCompare(b.name),
  updated: (a, b) => (b.lastUpdated ?? '').localeCompare(a.lastUpdated ?? ''),
  size: (a, b) => b.size - a.size,
  tags: (a, b) => b.tags - a.tags,
};

export function Repositories() {
  const overview = useLoad('overview', api.overview);
  const [search, setSearch] = useState('');
  const [sort, setSort] = useState<Sort>('name');
  const [showEmpty, setShowEmpty] = useState(false);

  const all = overview.data?.repositories ?? [];
  // A registry keeps the name of a repository after its last image is deleted.
  const emptyCount = useMemo(() => all.filter((r) => r.tags === 0 && !r.error).length, [all]);
  const visible = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return all
      .filter((r) => (showEmpty || r.tags > 0 || r.error) && r.name.toLowerCase().includes(needle))
      .sort(sorters[sort]);
  }, [all, search, sort, showEmpty]);
  const largest = Math.max(1, ...all.map((r) => r.size));
  const totals = overview.data?.totals;

  return (
    <>
      <div class="page-head">
        <div>
          <h1>Repositories</h1>
          <p class="muted">Everything stored in this registry.</p>
        </div>
        <RefreshButton loading={overview.loading} onClick={() => overview.reload(true)} />
      </div>

      <div class="stats">
        <Stat label="Repositories" value={totals && (totals.repositories - emptyCount).toLocaleString('en')} />
        <Stat label="Tags" value={totals?.tags.toLocaleString('en')} />
        <Stat label="Images" value={totals?.images.toLocaleString('en')} hint="Distinct image digests" />
        <Stat label="Storage" value={totals && formatBytes(totals.size)} hint="Compressed size of all tagged images. Layers shared between images are counted once." />
      </div>

      <div class="toolbar">
        <SearchBox value={search} onInput={setSearch} placeholder="Search repositories" />
        <span class="spacer" />
        {emptyCount > 0 && (
          <label class="check" title="Repositories whose images have all been deleted">
            <input type="checkbox" checked={showEmpty} onChange={(event) => setShowEmpty(event.currentTarget.checked)} />
            Show {emptyCount} empty
          </label>
        )}
        <Select
          label="Sort"
          value={sort}
          onChange={setSort}
          options={[
            ['name', 'Name'],
            ['updated', 'Last pushed'],
            ['size', 'Size'],
            ['tags', 'Tags'],
          ]}
        />
      </div>

      {overview.error && !overview.data ? (
        <ErrorMessage error={overview.error} retry={() => overview.reload()} />
      ) : !overview.data ? (
        <div class="card">
          <SkeletonRows />
        </div>
      ) : visible.length === 0 ? (
        <div class="card">
          {search ? (
            <Message icon="search" title="No matching repositories">
              Nothing matches “{search}”.
            </Message>
          ) : (
            <Message icon="inbox" title="No images yet">
              Push an image to this registry and it will show up here.
            </Message>
          )}
        </div>
      ) : (
        <div class="card table repositories" role="table">
          <div class="row row-head" role="row">
            <span role="columnheader">Repository</span>
            <span role="columnheader" class="num">Tags</span>
            <span role="columnheader" class="col-size">Size</span>
            <span role="columnheader" class="col-date">Last pushed</span>
          </div>
          {visible.map((repo) => (
            <Link href={repositoryPath(repo.name)} class="row row-link" key={repo.name}>
              <span class="cell-name">
                <Icon name="box" />
                <RepositoryName name={repo.name} />
                {repo.error && <span class="badge badge-danger" title={repo.error}>Error</span>}
              </span>
              <span class="num">{repo.tags.toLocaleString('en')}</span>
              <span class="col-size size">
                <span>{formatBytes(repo.size)}</span>
                <span class="bar">
                  <span style={{ width: `${Math.max(2, (repo.size / largest) * 100)}%` }} />
                </span>
              </span>
              <span class="col-date muted" title={formatDate(repo.lastUpdated)}>
                {formatAge(repo.lastUpdated)}
              </span>
            </Link>
          ))}
        </div>
      )}
      {overview.data && visible.length > 0 && <p class="table-foot muted">{plural(visible.length, 'repository', 'repositories')}</p>}
    </>
  );
}

/** Shows the namespace of "team/app" dimmed, so the eye lands on the image name. */
export function RepositoryName({ name }: { name: string }) {
  const slash = name.lastIndexOf('/') + 1;
  return (
    <span class="repo-name">
      {slash > 0 && <span class="muted">{name.slice(0, slash)}</span>}
      {name.slice(slash)}
    </span>
  );
}

function Stat({ label, value, hint }: { label: string; value?: string; hint?: string }) {
  return (
    <div class="stat" title={hint}>
      <span class="stat-label">{label}</span>
      {value === undefined ? <span class="skeleton stat-skeleton" /> : <span class="stat-value">{value}</span>}
    </div>
  );
}
