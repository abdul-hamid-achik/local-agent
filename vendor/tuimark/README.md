# Vendored `@thelacanians/tuimark`

`thelacanians-tuimark-0.4.5.tgz` is the npm package that Tuimark's v0.4.5
release workflow packed (its `npm-pack` job), unmodified. `apps/cli` depends on
it through a `file:` specifier because that version is not on the npm registry
yet. The package has no install scripts; at run time it uses `TUIMARK_BINARY`,
else a cached copy, else the release's checksum-verified binary.

Replace it with the registry version (`"@thelacanians/tuimark": "X.Y.Z"`) once
the release is published there, and delete this folder.

sha256 `d75e4ae4329555e3dc654f037659f963fd35cac7a0aba017d6321cfa9d62e71f`
