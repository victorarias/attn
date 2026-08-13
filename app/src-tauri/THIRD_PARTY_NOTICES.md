# Third-Party Notices

## tauri-plugin-webdriver

Parts of attn's browser automation executor and macOS JavaScript dialog handler
are adapted from `tauri-plugin-webdriver` 0.2.1:

https://github.com/Choochmeque/tauri-plugin-webdriver

The adapted code targets attn's dynamically created child `tauri::Webview`,
uses attn's authenticated daemon transport instead of the plugin's HTTP server,
and does not depend on the plugin at build or runtime.

Copyright (c) 2026 Vladimir Pankratov

Licensed under the MIT License. See `LICENSE.tauri-plugin-webdriver`.

## show-me (humanlayer/skills)

The attn agent skill's showing reference
(`internal/agent/attn_skill/references/showing.md`, embedded in the attn
binary and installed into agent skill directories) adapts the forms and
examples of the `show-me` skill:

https://github.com/humanlayer/skills

Copyright (c) 2026 HumanLayer

Licensed under the MIT License. See `LICENSE.humanlayer-skills`.
