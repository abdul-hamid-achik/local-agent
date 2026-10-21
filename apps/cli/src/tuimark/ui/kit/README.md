# Tuimark kit v2, vendored from Tuimark v0.4.5

`kit.tui`, `theme.tcss` and `kit.tcss` are copied unmodified from
`kit/` at the `v0.4.5` tag of `abdul-hamid-achik/tuimark`
(`git show v0.4.5:kit/kit.tui`). Do not edit them here. local-agent's own
styling and templates live in `../app.tcss` and `../app.tui`, which load after
the kit and win ties by order.

Used components: `k-confirm` (the approval dialog), `k-palette` (the command
palette), `k-help` (the help overlay) and `k-appbar` (the header), plus the
`k-btn`, `k-dialog` and `k-hints` classes. The kit's own documentation is
`kit/README.md` in the Tuimark repository.

To upgrade, copy the three files from the new tag, update the version above and
run `bun run tuimark:check`.
