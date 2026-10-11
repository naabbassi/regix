# Security

## Reporting a vulnerability

Please report security problems privately to the maintainers (through the repository's
"Report a vulnerability" button on GitHub) rather than in a public issue. Include what you found
and how to reproduce it. You will get an answer within a few days.

## What Regix protects

Regix holds registry credentials and can delete images. Its design keeps both out of reach of
anyone but the signed-in user:

| Concern | Measure |
|---|---|
| Credentials in the browser | Never sent there. The browser holds a random session ID in an `HttpOnly`, `SameSite=Strict` cookie, marked `Secure` over HTTPS. Credentials stay in server memory and are never written to disk or to the log. |
| Password guessing | After 8 failed sign-ins for a user from an address, further attempts are refused for 5 minutes. Unknown users take as long to reject as wrong passwords. |
| Cross-site request forgery | Every changing request must carry a custom header and JSON content type, which another site cannot send without a CORS preflight that Regix never approves. Requests that the browser marks as cross-site are refused. |
| Cross-site scripting | The interface renders all registry data as text. A strict Content-Security-Policy allows scripts and styles from Regix itself only, with no inline code. |
| Clickjacking | `frame-ancestors 'none'` and `X-Frame-Options: DENY`. |
| Request forgery against the registry | Repository names, tags and digests are validated against the registry's own grammar before they are used in a request, and pagination links are followed on the configured registry only. |
| Accidental deletion | Deletion states its consequences, requires typing the name, refuses tags that moved since the page was loaded, honours protected tags, and can be switched off (`REGIX_READ_ONLY`). Every deletion is logged with the user. |
| Stale sessions | Sessions end after `REGIX_AUTH_SESSION_TTL` without activity, at sign-out, when Regix restarts, and as soon as the registry stops accepting the user's credentials. |
| Container escape surface | The image has no shell, runs as a non-root user and needs no writable file system or capabilities. |

## What you need to do

- **Serve Regix over HTTPS** whenever it is reachable from more than your own machine. Sign-in
  sends a password; without TLS it travels in the clear.
- **Do not use `REGIX_AUTH_MODE=none` on an open network.** In that mode everyone who can reach
  Regix can use the service account, including for deletion unless `REGIX_READ_ONLY=true`.
- **Give service accounts only the access they need**, and pass their passwords as mounted secret
  files (`REGIX_REGISTRY_PASSWORD_FILE`).
- **Keep `REGIX_REGISTRY_INSECURE_TLS` off** outside of tests; use `REGIX_REGISTRY_CA_FILE` for a
  private certificate authority.

## Known limits

- The failed sign-in limit is kept per Regix process and per client address. Behind a proxy that
  hides client addresses, all clients share one address, so the limit is effectively per user name.
- Protected tags are enforced by Regix, not by the registry. Someone with registry credentials can
  still delete them with other tools.
