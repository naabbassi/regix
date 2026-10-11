import { useState } from 'preact/hooks';
import { api, type Session } from '../api';
import { Icon, Logo } from '../components';

export function Login({ session, onSignedIn }: { session: Session; onSignedIn: (session: Session) => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const submit = (event: Event) => {
    event.preventDefault();
    setBusy(true);
    setError('');
    api
      .login(username, password)
      .then(onSignedIn, (failure: Error) => {
        setError(failure.message);
        setPassword('');
      })
      .finally(() => setBusy(false));
  };

  return (
    <main class="centered">
      <form class="login" onSubmit={submit}>
        <Logo size={40} />
        <h1>Sign in to {session.title}</h1>
        <p class="muted">
          {session.authMode === 'registry' ? 'Use your container registry username and password.' : 'Use your Regix account.'}
        </p>
        {error && (
          <div class="alert" role="alert">
            <Icon name="alert" />
            {error}
          </div>
        )}
        <label class="field">
          <span>Username</span>
          <input
            name="username"
            autocomplete="username"
            autocapitalize="none"
            spellcheck={false}
            required
            autofocus
            value={username}
            onInput={(event) => setUsername(event.currentTarget.value)}
          />
        </label>
        <label class="field">
          <span>Password</span>
          <input
            name="password"
            type="password"
            autocomplete="current-password"
            required
            value={password}
            onInput={(event) => setPassword(event.currentTarget.value)}
          />
        </label>
        <button type="submit" class="button button-primary button-large" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </main>
  );
}
