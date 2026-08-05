# Control cohort (plumbline-cohort-v1)

This is the scientific control for Plumbline weekly crawls. Cohort members are
spawned **every week** regardless of version change, to catch description-only
drift that a version poll cannot see.

## Definition

A server id is in the cohort iff:

```
uint64(first 8 bytes of SHA-256("plumbline-cohort-v1|" + server_id)) mod 10000 < 500
```

- Cohort version string: `plumbline-cohort-v1`
- Modulus: 10000
- Threshold: 500 (~5% of the packages population)
- Selection depends on **nothing but** the server id and the cohort version
  string — no randomness, timestamps, or local state.

## Scope

The cohort is drawn from the **packages population** only (servers with
non-empty `packages[]`). Remotes-only servers are out of scope for v1.

## Freeze policy

The member list is frozen for the study. Do not add or remove members
mid-study. Servers that leave the registry are recorded as `departed`,
not replaced.

## Snapshot (generated)

- Packages population size at generation: **10738**
- Cohort members: **537**
- Member list file: [`cohort-v1-members.txt`](cohort-v1-members.txt)

Regenerate (operators only; do not commit a mid-study regen without a new
cohort version):

```bash
go run ./scripts/gen-cohort.go
```

Anyone can recompute membership for an arbitrary id with the definition
above, or by calling `crawler.InControlCohort(id)` in Go.
