# Public flip checklist (repo settings)

Workflow hardening is in `.github/workflows/`. Before making the repository
public, confirm these **GitHub UI / org settings** (cannot be set from this
checkout without `gh auth`):

1. **2FA** on the owner account (hardware key preferred).
2. **Branch protection** on `main`: no force-push; require PR or admin-only push as you prefer for a solo repo.
3. **Secret scanning** + **push protection** enabled.
4. **Private vulnerability reporting** enabled.
5. **Actions → Fork pull request workflows from outside collaborators**: require approval for first-time contributors (all fork PRs).

When the green-signal shell check is clean *and* these five are confirmed,
flip visibility to public. Week-1 evidence collection does **not** wait on
the flip.

## Known debts (recorded 2026-08-06)

Honest gaps. Known debt is fine; forgotten debt is what hurts.

| Debt | Status | Notes |
|------|--------|-------|
| **CI** | Open | Pre-push cross-*compiles* macOS/Windows but does not *run* tests there. Actions CI was believed green while never passing. First thing to buy back after week-1 starts. |
| **Signed releases** | Blocked | Sigstore keyless needs Actions OIDC. Until the budget/runner story is resolved, no signed binary — verifiability-over-trust is unavailable. Blocks CP-12, not week 1. |
| **Crawl host** | Open | Cohort spawns still run on the operator/dev machine. Signing key is offline; disposable VPS is the remaining mitigation. |

Do not quietly delete this section when flipping public.
