# CI and releases

Pull requests and pushes to `main` run formatting, vet, build, race-enabled
tests, coverage checks, lint, vulnerability scanning, and cross-compilation.
Tagged pushes run CI as well; the release workflow independently requires
formatting, vet, race-enabled tests, and the coverage gate to pass before packaging:

- Linux: `amd64`, `arm64`
- macOS: `amd64`, `arm64`
- Windows: `amd64`, `arm64`

To publish a release, push a version tag beginning with `v` after merging the
release changes:

```sh
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

The GitHub Actions release workflow attaches a compressed package for each
target and a `SHA256SUMS` file to the generated GitHub Release. Each package
includes the executable, `config.example.yaml`, the license, and the SFeed
protocol notes.
