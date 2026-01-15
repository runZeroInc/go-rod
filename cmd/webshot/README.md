# webshot

This is a small utility that demonstrates how to use go-rod to capture web screenshots
and javascript state for a given URL. This utility tries to run the web browser as a 
non-privileged user, with the sandbox enabled if possible, across Linux, macOS, and 
Windows.

This example uses the system browser by default, but can be configured using the environment:

Set `WEBSHOT_CHROMIUM_IGNORE_SYSTEM=true` to disable the system browser installation.

Set `WEBSHOT_CHROMIUM_AUTOMATIC_INSTALL=true` to automatically download binaries from Chrome-for-Testing or Puppeteer.