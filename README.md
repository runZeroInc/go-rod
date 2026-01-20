# go-rod

This is a stripped-down fork of @ysmood's [https://github.com/go-rod/rod](https://github.com/go-rod/rod) with the following changes:
- Inline and vendoring of the ysmood's other packages.
- Removal of leakless and automatic file drops (triggers anti-malware).
- Replacement of panic-based error handling with error return parameters.
- Switch to Chrome for Testing for most platforms, fallback to Puppeteer builds for Linux on ARM64.
- Support to run Chrome as a low-privileged user to avoid `--no-sandbox` issues as root on Linux/macOS.
- Removal of artifacts created by `go generate`.

Thank you very much to @ysmood and all go-rod contributors.

The focus of this fork is almost entirely on screenshots; a sample CLI is stored under the `cmd/webshot` path.

Your particular Go use case may be better handled by:
- The original [go-rod/rod](https://github.com/go-rod/rod) package.
- The new [Vibium Clicker](https://github.com/VibiumDev/vibium/tree/main/clicker) package.
- The mature [chromedp](https://github.com/chromedp/chromedp) package.
