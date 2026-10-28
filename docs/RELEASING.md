# CI and releases

Pull requests and pushes to `main` run formatting, vet, build, race-enabled
tests, coverage checks, lint, vulnerability scanning, and cross-compilation.
When all checks pass on `main`, the release workflow automatically publishes a
versioned GitHub Release. It uses Conventional Commit messages to choose the
next SemVer version:

- `feat:` increments the minor version.
- `fix:` and other commit types increment the patch version.
- `!` after the type/scope or a `BREAKING CHANGE:` footer increments the major
  version.
- Breaking changes take precedence over features; features take precedence over
  patches. Messages that do not follow Conventional Commits use a patch bump.
- The initial release starts from `v0.0.0`, so the first release is `v0.0.1`
  unless the commit history contains a feature or breaking-change commit.

Tagged pushes run CI as well. The release workflow independently requires
formatting, vet, race-enabled tests, and the coverage gate to pass before packaging:

- Linux: `amd64`, `arm64`
- macOS: `amd64`, `arm64`
- Windows: `amd64`, `arm64`

To publish a release manually, push a version tag beginning with `v`:

```sh
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

The GitHub Actions release workflow attaches a compressed package for each
target and a `SHA256SUMS` file to the generated GitHub Release. Each package
includes the executable, `config.example.yaml`, the license, and the SFeed
protocol notes.
