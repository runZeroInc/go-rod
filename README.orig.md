# Overview

[![Go Reference](https://pkg.go.dev/badge/github.com/runZeroInc/go-rod.svg)](https://pkg.go.dev/github.com/runZeroInc/go-rod)
[![Discord Chat](https://img.shields.io/discord/719933559456006165.svg)][discord room]

## [Documentation](https://go-rod.github.io/) | [API reference](https://pkg.go.dev/github.com/runZeroInc/go-rod?tab=doc) | [FAQ](https://go-rod.github.io/#/faq/README)

Rod is a high-level driver directly based on [DevTools Protocol](https://chromedevtools.github.io/devtools-protocol).
It's designed for web automation and scraping for both high-level and low-level use, senior developers can use the low-level packages and functions to easily
customize or build up their own version of Rod, the high-level functions are just examples to build a default version of Rod.

[中文 API 文档](https://pkg.go.dev/github.com/go-rod/go-rod-chinese)

## Features

- Chained context design, intuitive to timeout or cancel the long-running task
- Auto-wait elements to be ready
- Debugging friendly, auto input tracing, remote monitoring headless browser
- Thread-safe for all operations
- Automatically find or download [browser](lib/launcher)
- High-level helpers like WaitStable, WaitRequestIdle, HijackRequests, WaitDownload, etc
- Two-step WaitEvent design, never miss an event ([how it works](https://github.com/runZeroInc/go-rod/pkg/internal/goob))
- Correctly handles nested iframes or shadow DOMs
- No zombie browser process after the crash ([how it works](https://github.com/runZeroInc/go-rod/pkg/internal/leakless))
- [CI](https://github.com/runZeroInc/go-rod/actions) enforced 100% test coverage

## Examples

Please check the [examples_test.go](examples_test.go) file first, then check the [examples](lib/examples) folder.

For more detailed examples, please search the unit tests.
Such as the usage of method `HandleAuth`, you can search all the `*_test.go` files that contain `HandleAuth`,
for example, use GitHub online [search in repository](https://github.com/runZeroInc/go-rod/search?q=HandleAuth&unscoped_q=HandleAuth).
You can also search the GitHub [issues](https://github.com/runZeroInc/go-rod/issues) or [discussions](https://github.com/runZeroInc/go-rod/discussions),
a lot of usage examples are recorded there.

[Here is a comparison](lib/examples/compare-chromedp) of the examples between rod and Chromedp.

If you have questions, please raise an [issues](https://github.com/runZeroInc/go-rod/issues)/[discussions](https://github.com/runZeroInc/go-rod/discussions) or join the [chat room][discord room].

## Sponsors

Rod is sponsored by many organizations and individuals, thank you for your support!

Please contact [yad@ysmood.org](mailto:yad@ysmood.org) if you want to be listed here.

<!-- markdownlint-disable MD033 -->

<table style="border-collapse: collapse">
  <tr>
    <td>
      <p>Browser testing via</p>
      <a href="https://www.lambdatest.com/" target="_blank">
        <img
          src="https://www.lambdatest.com/blue-logo.png"
          alt="LambdaTest Logo"
          width="250"
          height="45"
        />
      </a>
    </td>
  </tr>
</table>

<!-- markdownlint-enable MD033 -->

## Join us

Your help is more than welcome! Even just open an issue to ask a question may greatly help others.

Please read [How To Ask Questions The Smart Way](http://www.catb.org/~esr/faqs/smart-questions.html) before you ask questions.

If you want to contribute please read the [Contributor Guide](.github/CONTRIBUTING.md).

[discord room]: https://discord.gg/CpevuvY
