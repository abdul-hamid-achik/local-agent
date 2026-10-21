# Vendored `@thelacanians/tuimark`

`thelacanians-tuimark-0.4.6-pre.tgz` is the npm package packed, unmodified, from the Tuimark v0.4d tree at commit `b5c7448`
(`sdk/ts`, version `0.4.6-pre`), before the v0.4.6 release exists. `apps/cli`
depends on it through a `file:` specifier because that version is not on the
npm registry yet. The package has no install scripts; at run time it uses
`TUIMARK_BINARY`, else a cached copy, else the release's checksum-verified
binary. The pre-release has no binary to download: set `TUIMARK_BINARY` to a
Tuimark build of the same tree (a `tuimark` 0.4.6 or newer).

It carries what the client uses from 0.4.6: `append` and `op: "append"` in a
batch, `commit`, and the `inline` and `screen` options of `spawnHost`.

Replace it with the released tarball, or with the registry version
(`"@thelacanians/tuimark": "X.Y.Z"`) once it is published there, and delete
this folder.

sha256 `aa7e7fed3f0494af3b6adadf8739a4d4cb382a68dfa846afef7c6a2f1c5e491b`
