import type { ComponentChildren } from 'preact';
import { useMemo, useState } from 'preact/hooks';
import { api, type Image, type Platform, type Session, type Tag } from '../api';
import { Breadcrumbs, Command, CopyButton, ErrorMessage, Icon, Message, SkeletonRows, toast } from '../components';
import { buildStep, formatAge, formatBytes, formatDate, imagePath, Link, navigate, plural, repositoryPath, shortDigest, useLoad } from '../lib';
import { DeleteDialog, describeDeletion } from './DeleteDialog';

type Tab = 'layers' | 'config' | 'labels' | 'manifest';

const kinds = { image: 'Image', index: 'Multi-platform image', artifact: 'Artifact' };

export function ImagePage({ repository, reference, session }: { repository: string; reference: string; session: Session }) {
  const image = useLoad(`${repository}@${reference}`, () => api.image(repository, reference));
  const crumbs = [{ label: 'Repositories', href: '/' }, { label: repository, href: repositoryPath(repository) }, { label: displayReference(reference) }];

  return (
    <>
      <Breadcrumbs items={crumbs} />
      {image.error && !image.data ? (
        image.error.message.startsWith('Not found') ? (
          <Message icon="search" title="Image not found" action={<Link href={repositoryPath(repository)} class="button">Back to {repository}</Link>}>
            <code>{reference}</code> does not exist in this repository. It may have been deleted.
          </Message>
        ) : (
          <ErrorMessage error={image.error} retry={() => image.reload()} />
        )
      ) : !image.data ? (
        <div class="card">
          <SkeletonRows rows={8} />
        </div>
      ) : (
        <Details image={image.data} reference={reference} session={session} />
      )}
    </>
  );
}

const displayReference = (reference: string) => (reference.includes(':') ? shortDigest(reference) : reference);

function Details({ image, reference, session }: { image: Image; reference: string; session: Session }) {
  const platforms = useMemo(() => image.platforms.filter((p) => !p.attestation), [image]);
  const attestations = image.platforms.length - platforms.length;
  const [platformIndex, setPlatformIndex] = useState(0);
  const [tab, setTab] = useState<Tab>('layers');
  const [deleting, setDeleting] = useState<Tag[]>();
  const platform: Platform | undefined = platforms[platformIndex];

  const byTag = !reference.includes(':');
  const base = `${session.pullHost}/${image.repository}`;
  const labels = Object.entries(platform?.config?.labels ?? {});
  const deletable = image.tags.filter((t) => !t.protected);
  const canDelete = !session.readOnly && image.tags.length > 0 && deletable.length === image.tags.length;

  return (
    <>
      <div class="page-head">
        <div>
          <h1 class="mono-title">
            <span class="muted">{image.repository}:</span>
            {displayReference(reference)}
          </h1>
          <p class="tag-list">
            <span class="badge">{kinds[image.kind]}</span>
            {image.tags.map((tag) => (
              <Link href={imagePath(image.repository, tag.name)} class={`chip chip-link ${tag.name === reference ? 'is-current' : ''}`}>
                <Icon name="tag" size={12} />
                {tag.name}
              </Link>
            ))}
            {image.tags.length === 0 && <span class="muted">Untagged</span>}
          </p>
        </div>
        {canDelete && (
          <button type="button" class="button button-danger-quiet" onClick={() => setDeleting(image.tags)}>
            <Icon name="trash" />
            Delete image
          </button>
        )}
      </div>

      <div class="facts">
        <Fact label="Digest">
          <code title={image.digest}>{shortDigest(image.digest)}</code>
          <CopyButton text={image.digest} label="Copy digest" />
        </Fact>
        <Fact label="Size" hint="Compressed, as stored and transferred">
          {formatBytes(platform?.size ?? image.size)}
        </Fact>
        <Fact label="Built" hint={formatDate(platform?.created ?? image.created)}>
          {formatAge(platform?.created ?? image.created)}
        </Fact>
        <Fact label="Layers">{platform ? platform.layers.filter((l) => l.digest).length : '—'}</Fact>
      </div>

      <div class="commands">
        {byTag && <Command text={`docker pull ${base}:${reference}`} />}
        <Command text={`docker pull ${base}@${image.digest}`} />
      </div>

      {platforms.length > 1 && (
        <div class="segmented" role="tablist" aria-label="Platform">
          {platforms.map((p, i) => (
            <button type="button" role="tab" aria-selected={i === platformIndex} class={i === platformIndex ? 'is-active' : ''} onClick={() => setPlatformIndex(i)}>
              {p.platform || shortDigest(p.digest)}
            </button>
          ))}
        </div>
      )}
      {attestations > 0 && (
        <p class="muted small">Includes {plural(attestations, 'build attestation')} (provenance or SBOM), counted in the total size of {formatBytes(image.size)}.</p>
      )}

      {platform?.error && (
        <div class="alert" role="alert">
          <Icon name="alert" />
          {platform.error}
        </div>
      )}

      <div class="tabs" role="tablist">
        {(
          [
            ['layers', `Layers`],
            ['config', 'Configuration'],
            ['labels', `Labels${labels.length ? ` (${labels.length})` : ''}`],
            ['manifest', 'Manifest'],
          ] as [Tab, string][]
        ).map(([id, label]) => (
          <button type="button" role="tab" aria-selected={tab === id} class={tab === id ? 'is-active' : ''} onClick={() => setTab(id)}>
            {label}
          </button>
        ))}
      </div>

      {tab === 'layers' && platform && <Layers platform={platform} />}
      {tab === 'config' && platform && <Configuration platform={platform} image={image} />}
      {tab === 'labels' && <KeyValues rows={labels} empty="This image has no labels." />}
      {tab === 'manifest' && <Json value={platforms.length > 1 || !platform?.manifest ? image.manifest : platform.manifest} />}

      {deleting && (
        <DeleteDialog
          repository={image.repository}
          tags={deleting}
          allTags={image.tags}
          onClose={() => setDeleting(undefined)}
          onDeleted={(results) => {
            const deleted = results.some((r) => r.action !== 'skipped');
            toast(describeDeletion(results), deleted ? 'success' : 'error');
            if (deleted) navigate(repositoryPath(image.repository), true);
          }}
        />
      )}
    </>
  );
}

function Fact({ label, hint, children }: { label: string; hint?: string; children: ComponentChildren }) {
  return (
    <div class="fact" title={hint}>
      <span class="stat-label">{label}</span>
      <span class="fact-value">{children}</span>
    </div>
  );
}

function Layers({ platform }: { platform: Platform }) {
  if (platform.layers.length === 0) return <div class="card"><Message icon="inbox" title="No layers" /></div>;
  const largest = Math.max(1, ...platform.layers.map((l) => l.size));
  let number = 0;
  return (
    <ol class="card layers">
      {platform.layers.map((layer) => {
        const step = buildStep(layer.createdBy);
        return (
          <li class={layer.digest ? '' : 'is-empty'}>
            <span class="layer-number">{layer.digest ? ++number : ''}</span>
            <div class="layer-main">
              <code class="layer-step" title={step}>{step || layer.mediaType || 'Layer'}</code>
              {layer.digest && (
                <span class="layer-meta muted">
                  <code>{shortDigest(layer.digest)}</code>
                  {layer.comment && <span>· {layer.comment}</span>}
                </span>
              )}
            </div>
            <span class="size">
              {layer.digest ? (
                <>
                  <span>{formatBytes(layer.size)}</span>
                  <span class="bar">
                    <span style={{ width: `${Math.max(2, (layer.size / largest) * 100)}%` }} />
                  </span>
                </>
              ) : (
                <span class="muted small">metadata</span>
              )}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function Configuration({ platform, image }: { platform: Platform; image: Image }) {
  const config = platform.config;
  const rows: [string, string | undefined][] = [
    ['Platform', platform.platform],
    ['Entrypoint', config?.entrypoint && JSON.stringify(config.entrypoint)],
    ['Command', config?.cmd && JSON.stringify(config.cmd)],
    ['Working directory', config?.workingDir],
    ['User', config?.user],
    ['Exposed ports', config?.exposedPorts?.join(', ')],
    ['Volumes', config?.volumes?.join(', ')],
    ['Stop signal', config?.stopSignal],
    ['Author', platform.author],
    ['Built', platform.created && formatDate(platform.created)],
    ['Artifact type', image.artifactType],
    ['Media type', platform.mediaType],
    ['Manifest digest', platform.digest],
  ];
  const env = (config?.env ?? []).map((entry): [string, string] => {
    const split = entry.indexOf('=');
    return split < 0 ? [entry, ''] : [entry.slice(0, split), entry.slice(split + 1)];
  });
  return (
    <>
      <KeyValues rows={rows.filter((row): row is [string, string] => !!row[1])} empty="" plainKeys />
      {env.length > 0 && (
        <>
          <h2 class="section-title">Environment</h2>
          <KeyValues rows={env} empty="" />
        </>
      )}
    </>
  );
}

function KeyValues({ rows, empty, plainKeys }: { rows: [string, string][]; empty: string; plainKeys?: boolean }) {
  if (rows.length === 0) return <div class="card"><Message icon="inbox" title={empty} /></div>;
  return (
    <dl class="card key-values">
      {rows.map(([key, value]) => (
        <div>
          <dt>{plainKeys ? key : <code>{key}</code>}</dt>
          <dd>
            <code>{value}</code>
          </dd>
        </div>
      ))}
    </dl>
  );
}

function Json({ value }: { value: unknown }) {
  const text = useMemo(() => JSON.stringify(value, null, 2), [value]);
  return (
    <div class="card json">
      <CopyButton text={text} label="Copy manifest" />
      <pre>{text}</pre>
    </div>
  );
}
