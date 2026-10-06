# Third-party software

The project's original code is licensed under Apache-2.0. Dependencies retain
their own licenses and copyright notices; the project license does not replace
them.

Pulumi is used under Apache-2.0 at commit
`21bf19ba40dba5dce0f565e8c614b5d8aa4dbb17`:
https://github.com/pulumi/pulumi/tree/21bf19ba40dba5dce0f565e8c614b5d8aa4dbb17
Its original notices are preserved in `third_party/pulumi`.

Container builds collect the Go dependencies' license texts, notices, and
source files required by licenses such as MPL-2.0 using `go-licenses save`.
These are included at `/usr/share/licenses/pulumid/dependencies` in the image.
The project's license and this document are in `/usr/share/licenses/pulumid`.
The Go toolchain license and patent notice are also included there.
Debian package copyright information is under `/usr/share/doc`.

The MPL-2.0 portions remain available under MPL-2.0, including the corresponding
sources bundled above. Upstream sources for the currently selected versions:

- https://github.com/hashicorp/errwrap/tree/v1.1.0
- https://github.com/hashicorp/go-multierror/tree/v1.1.1
- https://github.com/hashicorp/go-version/tree/v1.9.0
- https://github.com/hashicorp/hcl/tree/v2.24.0

The collector is a release aid, not a substitute for reviewing new dependencies
and any code copied into this project. Do not bypass collection errors when
publishing a release.

This is an independent project, not affiliated with or endorsed by Pulumi
Corporation. Pulumi is a trademark of Pulumi Corporation.
