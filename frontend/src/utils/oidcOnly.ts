/** Whether this frontend build should expose only OIDC login to regular users. */
export function isOidcOnlyEnabled(): boolean {
  return import.meta.env.VITE_OIDC_ONLY === 'true'
}

const OIDC_ONLY_BLOCKED_PATHS = ['/register', '/forgot-password', '/reset-password']

/** Resolve auth pages hidden by OIDC-only mode to the regular login page. */
export function getOidcOnlyRedirect(path: string): string | null {
  if (!isOidcOnlyEnabled()) return null
  return OIDC_ONLY_BLOCKED_PATHS.some(
    (blockedPath) => path === blockedPath || path.startsWith(`${blockedPath}/`)
  ) ? '/login' : null
}
