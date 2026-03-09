## CI/CD

- Before pushing code, run `act` to test GitHub Actions CI pipelines locally. `act` is installed via Homebrew.
- Before committing, check test coverage. Do not let coverage regress.

## Release Process

1. Bump `VERSION` in `Makefile` (e.g. `VERSION ?= 0.4.0`)
2. Commit: `git commit -am "chore: bump version to X.Y.Z"`
3. Push: `git push`
4. Tag: `git tag v<VERSION>` (lightweight tag, e.g. `git tag v0.4.0`)
5. Push tag: `git push origin v<VERSION>`
6. GitHub Actions (`release.yml`) automatically builds .deb packages (amd64 + arm64) and creates a GitHub Release with auto-generated release notes
7. To deploy to a server: `make deploy-deb TARGET_HOST=user@ip` or `make deploy-deb-arm64 TARGET_HOST=user@ip`
