# Tuimark kit v2, vendored from Tuimark v0.4.5

`kit.tui`, `theme.tcss` and `kit.tcss` are copied unmodified from
`kit/` at the `v0.4.5` tag of `abdul-hamid-achik/tuimark`
(`git show v0.4.5:kit/kit.tui`). Do not edit them here. local-agent's own
styling and templates live in `../app.tcss` and `../app.tui`, which load after
the kit and win ties by order.

Used templates: `k-confirm` (the approval dialog), `k-palette` (the command
palette), `k-appbar` (the header) and `k-empty` (the welcome screen). The help
overlay is local-agent's own modal dressed in the kit's `k-help` classes, and
the status row uses the `k-statusbar` class. The kit's own documentation
(`kit/README.md`), and the `kit/components/NAME.tui` examples `kit.tui`
points to, are in the Tuimark repository; they are not vendored here.

To upgrade, copy the three files from the new tag, update the version above and
run `bun run tuimark:check`.
