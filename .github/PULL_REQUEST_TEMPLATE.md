## Summary

<!-- Describe what this PR changes and why. Use Conventional Commits in the PR title (e.g. `feat: ...`, `fix: ...`, `security: ...`). -->

## Related Issue

<!-- Closes #123 -->

## Checklist

- [ ] PR title follows [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `perf:`, `security:`, `docs:`, `refactor:`, `deps:`)
- [ ] Backend passes `go vet ./...` and `go test -race -count=1 ./...`
- [ ] Frontend passes `npm run build` (`tsc --noEmit && vite build`)
- [ ] All 5 UI locales (`en`, `de`, `es`, `fr`, `it`) have 100% key parity (if UI copy changed)
- [ ] No secrets, tokens, or personal hostnames committed
