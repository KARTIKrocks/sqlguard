<!-- A PR body is a form, not a document; its sections start at h2 by design. -->
<!-- markdownlint-disable-next-line MD041 -->
## Summary

What does this PR change, and why?

Closes #<!-- issue number, if any -->

## Type of change

- [ ] Bug fix
- [ ] New detection rule
- [ ] New integration / parser
- [ ] Feature / enhancement
- [ ] Docs only
- [ ] Refactor / chore

## Checklist

- [ ] `make ci` passes (fmt-check, vet, lint, vuln, test-race, lint-docs) across all modules
- [ ] Added/updated tests (and, where practical, a failure-mode check)
- [ ] Updated docs under `website/docs/` with a version marker for anything new (`_0.3+_`, `_Added in 0.3._`, `// 0.3+`) — never `website/versioned_docs/`
- [ ] Updated `AGENTS.md` / `.sqlguard.example.yml` if a convention or config key changed
- [ ] Added an entry under `## [Unreleased]` in `CHANGELOG.md`
- [ ] No new third-party deps in `analyzer` / `middleware` / `reporter`
- [ ] Findings stay redaction-safe (no raw literals leak into a `Result`)

## Notes for reviewers

Anything reviewers should focus on — tricky areas, trade-offs, follow-ups.
