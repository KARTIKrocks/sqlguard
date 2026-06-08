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

- [ ] `make ci` passes (fmt-check, vet, lint, test-race) across all modules
- [ ] Added/updated tests (and, where practical, a failure-mode check)
- [ ] Updated docs as needed (`README.md`, `CLAUDE.md`, `.sqlguard.example.yml`)
- [ ] Added an entry under `## [Unreleased]` in `CHANGELOG.md`
- [ ] No new third-party deps in `analyzer` / `middleware` / `reporter`
- [ ] Findings stay redaction-safe (no raw literals leak into a `Result`)

## Notes for reviewers

Anything reviewers should focus on — tricky areas, trade-offs, follow-ups.
