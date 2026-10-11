// Presentational pieces used across pages.
import type { ComponentChildren, JSX } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { Link } from './lib';

// ---- Icons (16px, drawn on a 24px grid) ----------------------------------------------------

const paths = {
  search: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14ZM21 21l-5.1-5.1',
  copy: 'M9 9h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1V10a1 1 0 0 1 1-1ZM16 5V4a1 1 0 0 0-1-1H4a1 1 0 0 0-1 1v11a1 1 0 0 0 1 1h1',
  check: 'M4 12.5 9.5 18 20 6.5',
  trash: 'M4 7h16M10 11v6M14 11v6M6 7l1 12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1l1-12M9 7V4h6v3',
  sun: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8ZM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4',
  moon: 'M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z',
  refresh: 'M20 11a8 8 0 0 0-14.3-4.5L4 8M4 4v4h4M4 13a8 8 0 0 0 14.3 4.5L20 16M20 20v-4h-4',
  chevron: 'm9 6 6 6-6 6',
  box: 'M12 3 4 7v10l8 4 8-4V7l-8-4ZM4 7l8 4 8-4M12 11v10',
  layers: 'm12 3-9 5 9 5 9-5-9-5ZM3 12.5l9 5 9-5M3 17l9 5 9-5',
  lock: 'M7 11V8a5 5 0 0 1 10 0v3M6 11h12a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1v-8a1 1 0 0 1 1-1Z',
  logout: 'M15 4h4a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-4M10 16l-4-4 4-4M6 12h10',
  alert: 'M12 9v4M12 17h.01M10.3 4.3 2.6 17.5A2 2 0 0 0 4.3 20.5h15.4a2 2 0 0 0 1.7-3L13.7 4.3a2 2 0 0 0-3.4 0Z',
  close: 'M6 6l12 12M18 6 6 18',
  tag: 'M3 12V4a1 1 0 0 1 1-1h8l9 9-9 9-9-9ZM7.5 7.5h.01',
  inbox: 'M3 13h5l1.5 3h5L16 13h5M5 5h14l2 8v5a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1v-5l2-8Z',
};

export function Icon({ name, size = 16 }: { name: keyof typeof paths; size?: number }) {
  return (
    <svg class="icon" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path d={paths[name]} />
    </svg>
  );
}

export function Logo({ size = 24 }: { size?: number }) {
  return (
    <svg class="logo" width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="8" />
      <path class="logo-top" d="M16 7 7 11.5l9 4.5 9-4.5L16 7Z" />
      <path class="logo-lines" d="m7 16 9 4.5 9-4.5M7 20.5 16 25l9-4.5" />
    </svg>
  );
}

// ---- Copying -------------------------------------------------------------------------------

function useCopy(text: string): [boolean, () => void] {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };
  return [copied, copy];
}

export function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, copy] = useCopy(text);
  return (
    <button
      type="button"
      class="icon-button"
      title={copied ? 'Copied' : label}
      aria-label={label}
      onClick={(event) => {
        event.stopPropagation();
        copy();
      }}
    >
      <Icon name={copied ? 'check' : 'copy'} />
    </button>
  );
}

/** A command the user is meant to copy, e.g. `docker pull …`. */
export function Command({ text }: { text: string }) {
  return (
    <div class="command">
      <code>{text}</code>
      <CopyButton text={text} label="Copy command" />
    </div>
  );
}

// ---- Page furniture ------------------------------------------------------------------------

export function Breadcrumbs({ items }: { items: { label: string; href?: string }[] }) {
  return (
    <nav class="breadcrumbs" aria-label="Breadcrumb">
      {items.map((item, i) => (
        <>
          {i > 0 && <Icon name="chevron" size={14} />}
          {item.href ? <Link href={item.href}>{item.label}</Link> : <span aria-current="page">{item.label}</span>}
        </>
      ))}
    </nav>
  );
}

export function SearchBox(props: { value: string; onInput: (value: string) => void; placeholder: string }) {
  const input = useRef<HTMLInputElement>(null);
  // "/" focuses the search box, as on most developer tools.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const typing = /^(INPUT|TEXTAREA|SELECT)$/.test((event.target as HTMLElement).tagName);
      if (event.key === '/' && !typing && !document.querySelector('dialog[open]')) {
        event.preventDefault();
        input.current?.focus();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  return (
    <label class="search">
      <Icon name="search" />
      <input
        ref={input}
        type="search"
        value={props.value}
        placeholder={props.placeholder}
        aria-label={props.placeholder}
        spellcheck={false}
        onInput={(event) => props.onInput(event.currentTarget.value)}
        onKeyDown={(event) => event.key === 'Escape' && (props.onInput(''), event.currentTarget.blur())}
      />
      <kbd>/</kbd>
    </label>
  );
}

export function Select<T extends string>(props: {
  label: string;
  value: T;
  options: [T, string][];
  onChange: (value: T) => void;
}) {
  return (
    <label class="select">
      <span>{props.label}</span>
      <select value={props.value} onChange={(event) => props.onChange(event.currentTarget.value as T)}>
        {props.options.map(([value, label]) => (
          <option value={value}>{label}</option>
        ))}
      </select>
    </label>
  );
}

export function RefreshButton({ loading, onClick }: { loading: boolean; onClick: () => void }) {
  return (
    <button type="button" class={`button ${loading ? 'is-busy' : ''}`} onClick={onClick} disabled={loading} title="Reload from the registry">
      <Icon name="refresh" />
      <span class="hide-narrow">Refresh</span>
    </button>
  );
}

export function Message(props: {
  icon: 'alert' | 'inbox' | 'search';
  title: string;
  children?: ComponentChildren;
  action?: JSX.Element;
}) {
  return (
    <div class="message">
      <div class="message-icon">
        <Icon name={props.icon} size={20} />
      </div>
      <h2>{props.title}</h2>
      {props.children && <p>{props.children}</p>}
      {props.action}
    </div>
  );
}

export function ErrorMessage({ error, retry }: { error: Error; retry: () => void }) {
  return (
    <Message
      icon="alert"
      title="Something went wrong"
      action={
        <button type="button" class="button" onClick={retry}>
          Try again
        </button>
      }
    >
      {error.message}
    </Message>
  );
}

/** Placeholder rows shown while a table loads. */
export function SkeletonRows({ rows = 6 }: { rows?: number }) {
  return (
    <div class="skeleton-rows" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <div class="skeleton-row">
          <span class="skeleton" style={{ width: `${28 + ((i * 17) % 30)}%` }} />
          <span class="skeleton" style={{ width: '12%' }} />
        </div>
      ))}
    </div>
  );
}

export function PlatformChips({ platforms }: { platforms?: string[] }) {
  if (!platforms?.length) return <span class="muted">—</span>;
  const shown = platforms.slice(0, 3);
  return (
    <span class="chips">
      {shown.map((platform) => (
        <span class="chip">{platform.replace(/^linux\//, '')}</span>
      ))}
      {platforms.length > shown.length && <span class="chip" title={platforms.join(', ')}>+{platforms.length - shown.length}</span>}
    </span>
  );
}

// ---- Toasts --------------------------------------------------------------------------------

interface Toast {
  id: number;
  text: string;
  tone: 'success' | 'error';
}

const TOAST = 'regix:toast';

export function toast(text: string, tone: Toast['tone'] = 'success') {
  window.dispatchEvent(new CustomEvent(TOAST, { detail: { text, tone } }));
}

export function Toasts() {
  const [toasts, setToasts] = useState<Toast[]>([]);
  useEffect(() => {
    let next = 0;
    const add = (event: Event) => {
      const id = next++;
      setToasts((all) => [...all, { id, ...(event as CustomEvent).detail }]);
      setTimeout(() => setToasts((all) => all.filter((t) => t.id !== id)), 5000);
    };
    window.addEventListener(TOAST, add);
    return () => window.removeEventListener(TOAST, add);
  }, []);
  return (
    <div class="toasts" role="status" aria-live="polite">
      {toasts.map((t) => (
        <div class={`toast toast-${t.tone}`} key={t.id}>
          <Icon name={t.tone === 'success' ? 'check' : 'alert'} />
          {t.text}
        </div>
      ))}
    </div>
  );
}
