# Deferred ideas — do not build during Phase E

CP-10 rule: resist every feature idea. Write it here. Do not open it.

Each entry is a reminder that shipping evidence beats shipping surface area.

1. **WebSocket live tail of the Merkle log** — nice for demos; not needed to
   evaluate KT-1/KT-2.

2. **Email / Slack alerts on HIGH+ changes** — operational candy; weekly hand
   review is the evidence path for now.

3. **Browser extension that badges MCP configs with drift** — product, not
   evidence. Revisit after six weeks of labelled data.

4. **Multi-region crawl mirrors with federated consistency proofs** — cool CT
   lore; one honest operator log is enough for the kill tests.

5. **Machine-learning “similarity” for near-miss descriptions** — explicitly
   forbidden by taxonomy-v1; do not sneak it in under another name.

6. **Automatic PR bot that opens issues on publishers when metadata drifts** —
   adversarial/noisy; hand labels first.

7. **Pretty PDF “weekly threat report” generator** — launch bait. Notebook +
   JSONL labels are the artifacts that get cited.

8. **Hosted SaaS dashboard with accounts and API keys** — CP-9 forbids user
   accounts on the public log; do not add them on a side door.

9. **Crawl of non-registry / private MCP servers** — out of policy
   (docs/CRAWLING.md). Opt-in private observation is a different product.

10. **Mobile app for verify-log** — CLI + docs already make the log
    independently verifiable.

11. **Rewriting the classifier in Rust “for speed”** — no evidence it is the
    bottleneck; taxonomy fixtures matter more than language cosplay.

12. **Community voting / reputation on servers** — social layer, not a
    transparency log. Contaminates KT framing.

13. **One-click “accept all cosmetic drifts” in plumb check** — convenience
    that teaches the wrong habit before we know cosmetic precision.

14. **NFT / badge for “verified unchanged for 30 days”** — do not.

15. **Integrating Plumbline scores into MCP registry ranking** — political and
    premature until KT-1/KT-2 pass with published numbers.

When Phase E ends and kill tests are scored, reopen this file deliberately —
not because an idea felt urgent in week two.

16. **Retrospective: CP-8 DoD tested containment, not observation.** Week-0
    dry runs found five self-inflicted crawler bugs (archive-as-argv0, missing
    install, PyPI entrypoint first-dist-info, discarded stderr, exit_code
    omitempty / EOF race) by hand probe, not by checkpoint tests. Containment
    was green while the crawler could not actually observe. Next checkpoint:
    DoD must include an ordinary successful spawn that returns tools *and* a
    failing spawn that records both stderr and exit status — not only the
    scary isolation property.
