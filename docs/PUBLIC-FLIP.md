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
