# Go and Wails use one dispatcher with the Tauri bindings as contract

The desktop backend is Go hosted by Wails v3. A single `CommandService.Invoke` dispatcher accepts the existing command names and JSON arguments; the frontend's tauri-specta `bindings.ts` remains the frozen contract, and Vite aliases route the Tauri core, event, opener, and dialog imports through Wails adapters. This keeps product behavior and the frontend contract stable while replacing the desktop runtime.

Wails opens the same `~/.ai-mission-manager/mission-manager.sqlite` database as the Tauri app and runs as a single instance. The two apps must never run simultaneously against that database. Wails does not migrate legacy schemas: when the schema is not current, open it with Tauri once so it can perform its existing migration, then close Tauri before starting Wails. This limited compatibility policy follows ADR-0008; it does not promise general schema compatibility between versions.
