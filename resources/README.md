# Packaged Resources

This directory contains source assets and generated runtime bundles used by Electron packaging.

- `icons/`: tracked application branding used by `electron-builder`.
- `python/`: generated embedded CPython runtime, ignored by Git.
- `camoufox/`: generated Camoufox browser bundle, ignored by Git.

Generate the ignored runtime bundles with `npm run release:pack-python` and
`npm run release:pack-camoufox`.
