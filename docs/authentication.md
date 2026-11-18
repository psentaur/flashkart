# Login and staff access

Flashkart uses Clerk for public registration, verified email/password sign-in, Google sign-in, password reset, and sessions. Storefront browsing is public. Customers can purchase and view their own Activity. Admins can additionally use Analytics, Administration, and Replicas. Only the configured owner can use Team.

## Local setup

Create or select a Clerk development application. Configure email and password authentication, require email verification at registration, enable Google, and keep registration public. Apply `deploy/clerk/development.json` with the Clerk CLI after linking the application:

```sh
npx -y clerk@latest config patch --file deploy/clerk/development.json
cp .env.example .env
```

If the application was provisioned during setup, its keys are already saved in the ignored root `.env`; keep that file instead of copying the example over it. Accountless applications must first be claimed with `npx -y clerk@latest auth login` from the project root.

Configure these values in the root `.env`, which the local launcher loads without executing shell code:

| Variable | Purpose |
| --- | --- |
| `FLASHKART_AUTH_MODE=clerk` | Backend authentication mode |
| `VITE_AUTH_MODE=clerk` | Frontend authentication mode; the launcher keeps it aligned |
| `VITE_CLERK_PUBLISHABLE_KEY` | Public key used by the browser |
| `CLERK_SECRET_KEY` | Backend-only key for user management and invitations |
| `CLERK_ISSUER_URL` | Clerk Frontend API origin, such as `https://example.clerk.accounts.dev` |
| `FLASHKART_ALLOWED_ORIGINS` | Comma-separated exact frontend origins; local default includes `http://127.0.0.1:5173` and `http://localhost:5173` |
| `FLASHKART_OWNER_USER_ID` | Immutable Clerk `user_...` ID of the owner; initially empty |
| `FLASHKART_FRONTEND_URL` | Optional invitation destination origin; defaults to the first allowed origin |

Never prefix a secret with `VITE_`. Only the identity service receives the Clerk secret key; other services verify signed sessions against cached public JWKS. The Clerk issuer and allowed origins are checked explicitly. API requests use bearer session tokens obtained afresh through Clerk's `getToken()`; Flashkart does not implement its own refresh-token storage.

```sh
python3 deploy/local/dev.py start
python3 deploy/local/dev.py status
python3 deploy/local/dev.py stop
```

Open <http://127.0.0.1:5173/login>. The launcher starts the database, migrations, replicas, identity API on port 8209, marketplace services, and frontend. Existing queue/table settings still enable checkout, notifications, and analytics. Logs are in `build/local/market.log` and `build/local/web.log`. Stopping preserves database volumes and the Clerk application.

## Assign the owner and add admins

1. Register and verify the intended owner's account using the login page.
2. Find that person's Clerk user ID in the Clerk Dashboard. Public registration always creates an ordinary customer; the first person to register never automatically gains ownership.
3. Run the one-time local bootstrap and restart the backend:

```sh
python3 deploy/local/set-owner.py user_REPLACE_WITH_OWNER_ID
python3 deploy/local/dev.py stop
python3 deploy/local/dev.py start
```

The bootstrap verifies the account and its primary email before saving the owner ID to `.env`. It refuses to replace an existing different owner. Deployment operators configure the same ID in the production backend environment. Account deletion and ownership transfer remain operator tasks outside Team.

The owner sees **Team**. Search registered users by email, then confirm **Grant admin access** or **Remove admin access**. Unverified, banned, and locked users cannot be promoted. Demotion retains the customer account and all purchases. Only `admin` and `customer` can be assigned through the API; the owner cannot be demoted there.

For new people, **Invite an admin** sends a Clerk invitation valid for seven days. Its server-controlled invitation metadata becomes `publicMetadata.role = "admin"` when the recipient accepts and registers. Existing accounts must be promoted explicitly. Duplicate pending requests return the existing invitation without sending a second email; revoke pending invitations in Team. Expired invitations can be replaced. Someone who registers separately without accepting an invitation remains a customer and can be promoted by the owner.

The Clerk session configuration must include the custom claim:

```json
{ "role": "{{user.public_metadata.role}}" }
```

Only backend-controlled public metadata grants admin access. Browser-writable unsafe metadata is ignored. Ownership comes from the configured user ID, irrespective of metadata. Role changes take effect when short-lived tokens refresh (the supplied configuration uses 60-second tokens); the frontend rechecks identity every 30 seconds. Team mutations are audit-logged with actor, target, action, and outcome, without session tokens or secret keys.

## Google and production deployment

Clerk's development Google connection uses shared development credentials. Production requires a production Clerk instance and your own Google OAuth client credentials, configured in Clerk's Dashboard. Follow the [Clerk Google setup](https://clerk.com/docs/guides/configure/auth-strategies/social-connections/google), configure the production application domain and Clerk DNS, and register Clerk's exact Google redirect URI. Google secrets stay in Clerk.

Clerk links verified accounts sharing an email, so password and Google sign-in use the same user ID, orders, and permissions. See [Clerk account linking](https://clerk.com/docs/guides/configure/auth-strategies/social-connections/account-linking). MFA enrollment is outside this version.

Build production frontend assets with `VITE_AUTH_MODE=clerk` and the production `VITE_CLERK_PUBLISHABLE_KEY`. Supply the production issuer, HTTPS allowed origins, owner user ID, and secret key to `deploy/aws/gitops.sh`; it creates separate public authentication configuration and backend-secret Kubernetes Secrets. Only `identity-api` receives `clerk-secret`. AWS deployments reject demo authentication. CloudFront and nginx serve SPA paths while `/api` routes stay on their respective backend services. Production publishable keys are build-time inputs, so rebuild both the web container and S3 assets when changing Clerk instances.

The AWS smoke/load tools require current Clerk session tokens for their admin and customer fixtures, provided under their existing `FLASHKART_ADMIN_TOKEN` and `FLASHKART_CUSTOMER_TOKEN` test variable names. These are test credentials, not deployed shared secrets. For runs longer than a token lifetime, use the renewable browser-session helper described below. Analytics requests now require the admin session.

For long deployment acceptance/load runs, capture Playwright browser storage states for dedicated admin and customer test users (each context's `storageState({path: ...})`). Keep the files mode 0600 outside version control. The helper validates their roles, keeps Clerk running to refresh tokens, and provides a random-path loopback token endpoint to the Python and k6 fixtures. It does not relax the production origin checks or create long-lived bearer tokens:

```sh
BASE_URL=https://your-app.example \
CLERK_TEST_ADMIN_STATE=/private/admin-state.json \
CLERK_TEST_CUSTOMER_STATE=/private/customer-state.json \
node web/tests/clerk/run-with-sessions.mjs -- sh deploy/aws/acceptance.sh acceptance
```

Existing demo orders keep their demo customer IDs and are not reassigned to new Clerk accounts. The existing text ID columns and queue/event schemas accept Clerk IDs without a database migration.

## Tests and local demo mode

```sh
go test ./...
npm --prefix web run build
cd web
npm exec -- playwright test tests/auth.spec.ts tests/admin.spec.ts
npm exec -- playwright test --config playwright.clerk.config.ts
```

The first browser suite uses mocked role responses in explicit demo frontend mode. The Clerk suite needs the root `.env` and the locally running Clerk stack; it creates isolated development test users and removes them afterward. It verifies the actual login components, signed session claims, and backend role enforcement. Completing Google consent with a real Google account remains an interactive check.

For existing local integration fixtures, run `FLASHKART_AUTH_MODE=demo python3 deploy/local/dev.py start`. Defaults are `local-admin` and `local-customer`; Access tokens appears only in this explicitly selected demo mode. CI and kind fixtures also select demo mode explicitly. Never enable demo authentication in an AWS deployment.
