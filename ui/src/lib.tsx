// Small building blocks shared by the pages: routing, data loading and formatting.
import type { ComponentChildren } from 'preact';
import { useCallback, useEffect, useRef, useState } from 'preact/hooks';

// ---- Routing -------------------------------------------------------------------------------
// Repository names contain slashes, so "/-/" separates a repository from a tag or digest,
// as in /r/team/app/-/v1.2.

export type Route =
  | { page: 'repositories' }
  | { page: 'repository'; repository: string }
  | { page: 'image'; repository: string; reference: string }
  | { page: 'not-found' };

export const repositoryPath = (repository: string) => `/r/${repository}`;
export const imagePath = (repository: string, reference: string) => `/r/${repository}/-/${reference}`;

export function parseRoute(pathname: string): Route {
  if (pathname === '/') return { page: 'repositories' };
  if (!pathname.startsWith('/r/')) return { page: 'not-found' };
  const [repository, reference, ...rest] = decodeURIComponent(pathname.slice(3)).replace(/\/$/, '').split('/-/');
  if (!repository || rest.length > 0) return { page: 'not-found' };
  return reference ? { page: 'image', repository, reference } : { page: 'repository', repository };
}

const NAVIGATED = 'regix:navigated';

export function navigate(path: string, replace = false) {
  history[replace ? 'replaceState' : 'pushState'](null, '', path);
  window.dispatchEvent(new Event(NAVIGATED));
  window.scrollTo(0, 0);
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(location.pathname));
  useEffect(() => {
    const update = () => setRoute(parseRoute(location.pathname));
    window.addEventListener('popstate', update);
    window.addEventListener(NAVIGATED, update);
    return () => {
      window.removeEventListener('popstate', update);
      window.removeEventListener(NAVIGATED, update);
    };
  }, []);
  return route;
}

/** An in-app link: a real anchor, so opening in a new tab and copying the address still work. */
export function Link(props: { href: string; class?: string; title?: string; children: ComponentChildren }) {
  const onClick = (event: MouseEvent) => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    navigate(props.href);
  };
  return (
    <a href={props.href} class={props.class} title={props.title} onClick={onClick}>
      {props.children}
    </a>
  );
}

// ---- Data loading --------------------------------------------------------------------------

export interface Loaded<T> {
  data?: T;
  error?: Error;
  /** True during any load; `data` keeps the previous result while a reload runs. */
  loading: boolean;
  reload: (fresh?: boolean) => void;
  /** Replaces the data without asking the server, e.g. after a change whose outcome is known. */
  set: (data: T) => void;
}

/** Loads data when the component mounts and whenever `key` changes. */
export function useLoad<T>(key: string, load: (fresh: boolean) => Promise<T>): Loaded<T> {
  const [state, setState] = useState<{ key: string; data?: T; error?: Error; loading: boolean }>({ key, loading: true });
  const latest = useRef(0);
  const loader = useRef(load);
  loader.current = load;

  const run = useCallback(
    (fresh = false) => {
      const ticket = ++latest.current;
      setState((s) => (s.key === key ? { ...s, loading: true, error: undefined } : { key, loading: true }));
      loader.current(fresh).then(
        (data) => ticket === latest.current && setState({ key, data, loading: false }),
        (error) => ticket === latest.current && setState((s) => ({ ...s, key, error, loading: false })),
      );
    },
    [key],
  );
  useEffect(() => run(), [run]);

  const set = useCallback((data: T) => setState({ key, data, loading: false }), [key]);
  // While the key is changing, the state still belongs to the previous key: show nothing of it.
  return state.key === key ? { ...state, reload: run, set } : { loading: true, reload: run, set };
}

// ---- Formatting ----------------------------------------------------------------------------

export function formatBytes(bytes: number): string {
  if (!bytes) return '0 B';
  const units = ['B', 'kB', 'MB', 'GB', 'TB'];
  const exponent = Math.min(Math.floor(Math.log10(bytes) / 3), units.length - 1);
  const value = bytes / 1000 ** exponent;
  return `${value >= 100 || exponent === 0 ? Math.round(value) : value.toFixed(1)} ${units[exponent]}`;
}

const relative = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
const absolute = new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'short' });

export function formatAge(iso?: string): string {
  if (!iso) return '—';
  const seconds = (new Date(iso).getTime() - Date.now()) / 1000;
  const steps: [Intl.RelativeTimeFormatUnit, number][] = [
    ['year', 31536000],
    ['month', 2592000],
    ['day', 86400],
    ['hour', 3600],
    ['minute', 60],
  ];
  for (const [unit, size] of steps) {
    if (Math.abs(seconds) >= size) return relative.format(Math.round(seconds / size), unit);
  }
  return 'just now';
}

export const formatDate = (iso?: string) => (iso ? absolute.format(new Date(iso)) : '');

/** sha256:0123456789ab… → 0123456789ab */
export const shortDigest = (digest: string) => digest.replace(/^[a-z0-9]+:/, '').slice(0, 12);

export const plural = (count: number, one: string, many = one + 's') => `${count.toLocaleString('en')} ${count === 1 ? one : many}`;

/** A Dockerfile-like line from an image history entry. */
export function buildStep(createdBy = ''): string {
  return createdBy
    .replace(/^\/bin\/sh -c #\(nop\)\s+/, '')
    .replace(/^\/bin\/sh -c /, 'RUN ')
    .replace(/^\|\d+ (?:\S+=\S* )*\/bin\/sh -c /, 'RUN ')
    .replace(/ # buildkit$/, '')
    .trim();
}
