import { useEffect, useState } from 'preact/hooks';
import { api, SIGNED_OUT, type Session } from './api';
import { Icon, Logo, Message, Toasts } from './components';
import { Link, useRoute } from './lib';
import { ImagePage } from './pages/Image';
import { Login } from './pages/Login';
import { Repositories } from './pages/Repositories';
import { Repository } from './pages/Repository';

export function App() {
  const [session, setSession] = useState<Session>();
  const [error, setError] = useState<Error>();

  const load = () => {
    setError(undefined);
    api.session().then(setSession, setError);
  };
  useEffect(load, []);
  // Any request may find that the session has ended; fall back to the sign-in page.
  useEffect(() => {
    const onSignedOut = () => setSession((s) => s && { ...s, authenticated: false, user: undefined });
    window.addEventListener(SIGNED_OUT, onSignedOut);
    return () => window.removeEventListener(SIGNED_OUT, onSignedOut);
  }, []);
  useEffect(() => {
    if (session) document.title = session.title;
  }, [session?.title]);

  if (error) {
    return (
      <main class="centered">
        <Message
          icon="alert"
          title="Regix is not responding"
          action={
            <button type="button" class="button" onClick={load}>
              Try again
            </button>
          }
        >
          {error.message}
        </Message>
      </main>
    );
  }
  if (!session) return null;
  if (!session.authenticated) return <Login session={session} onSignedIn={setSession} />;
  return <Shell session={session} onSignedOut={load} />;
}

function Shell({ session, onSignedOut }: { session: Session; onSignedOut: () => void }) {
  const route = useRoute();
  return (
    <>
      <header class="topbar">
        <div class="topbar-inner">
          <Link href="/" class="brand">
            <Logo />
            <span>{session.title}</span>
          </Link>
          <span class="registry" title="The registry this Regix manages">
            <span class="dot" />
            {session.pullHost}
          </span>
          {session.readOnly && (
            <span class="badge" title="Deleting is switched off on this Regix">
              <Icon name="lock" size={12} />
              Read-only
            </span>
          )}
          <span class="spacer" />
          <ThemeToggle />
          {session.authMode !== 'none' && (
            <>
              <span class="user hide-narrow">{session.user}</span>
              <button type="button" class="icon-button" title="Sign out" aria-label="Sign out" onClick={() => api.logout().then(onSignedOut)}>
                <Icon name="logout" />
              </button>
            </>
          )}
        </div>
      </header>
      <main class="page">
        {route.page === 'repositories' && <Repositories />}
        {route.page === 'repository' && <Repository key={route.repository} repository={route.repository} session={session} />}
        {route.page === 'image' && (
          <ImagePage key={`${route.repository}@${route.reference}`} repository={route.repository} reference={route.reference} session={session} />
        )}
        {route.page === 'not-found' && (
          <Message icon="search" title="Page not found" action={<Link href="/" class="button">All repositories</Link>}>
            There is nothing at this address.
          </Message>
        )}
      </main>
      <footer class="footer">Regix {session.version}</footer>
      <Toasts />
    </>
  );
}

function ThemeToggle() {
  const [dark, setDark] = useState(() => document.documentElement.dataset.theme === 'dark');
  const toggle = () => {
    const next = !dark;
    document.documentElement.dataset.theme = next ? 'dark' : 'light';
    try {
      localStorage.setItem('regix-theme', next ? 'dark' : 'light');
    } catch {
      // Without storage the choice simply lasts until the page is closed.
    }
    setDark(next);
  };
  const label = dark ? 'Switch to light theme' : 'Switch to dark theme';
  return (
    <button type="button" class="icon-button" title={label} aria-label={label} onClick={toggle}>
      <Icon name={dark ? 'sun' : 'moon'} />
    </button>
  );
}
