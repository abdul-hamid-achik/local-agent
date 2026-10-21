# Vendored `@thelacanians/tuimark` 0.4.6

`thelacanians-tuimark-0.4.6.tgz` is the released `@thelacanians/tuimark`
0.4.6 package: the tarball the tuimark release workflow packed for tag
`v0.4.6` (tuimark@d041ec4, run 39643443178, artifact
`tuimark-npm-package`), with the release's `checksums.txt` embedded by the
workflow. Its sha256 is
`2ff907e365c8ffde8f8e1fb30be04d507792345c483cf33d5684c538646d9411`
(sha1 `fdd8024ecb0260a0ae29478d44da1a3bc4ffb639`, the shasum the publish
job printed). npm publication of 0.4.6 failed with E404 because the npm
trusted publisher is not configured yet, so the registry still only has
0.4.2 and the tarball stays vendored here. `apps/cli` depends on it as a
`file:` dependency.

Replace it with the registry version (`"@thelacanians/tuimark": "0.4.6"`)
and delete this folder once 0.4.6 is published to npm.
