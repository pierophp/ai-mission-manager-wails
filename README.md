# AI Mission Manager

Aplicativo desktop com Go + Wails v3, React, TypeScript e Vite+ (`vp`), uma toolchain unificada que reúne Vite, Rolldown, Vitest, Oxlint, Oxfmt e Vite Task. O projeto está configurado para desktop (macOS, Windows e Linux); não inclui alvos nem tarefas de build mobile.

## Requisitos

- Go 1.25 ou superior
- Node.js e npm (o `vite-plus` local disponibiliza a CLI `vp`; não exige instalação global)
- Wails CLI v3 (`go install github.com/wailsapp/wails/v3/cmd/wails3@latest`)
- Dependências nativas da plataforma: veja [instalação do Wails](https://v3.wails.io/quick-start/installation/)

## Desenvolvimento

```sh
wails3 dev
```

Para executar só o frontend, use `npm --prefix frontend run dev`. A checagem do frontend pode ser executada com `cd frontend && npm exec -- vp check`.

## Build desktop

```sh
wails3 build
```

O frontend fica em `frontend/`, o backend Go começa em `main.go` e as configurações de build ficam em `build/`.

## Banco de dados e migração do Tauri

O Wails usa o mesmo banco do app Tauri em `~/.ai-mission-manager/mission-manager.sqlite`. Execute apenas uma instância do Mission Manager por vez: nunca deixe os apps Tauri e Wails abertos simultaneamente sobre esse banco. O app Wails permite apenas uma instância aberta por vez.

Se o banco ainda precisar de uma migração de schema, abra-o pelo app Tauri primeiro e feche o Tauri antes de iniciar o Wails. O Wails recusa schemas legados que não reconhece; ele não executa as migrações antigas do Tauri.
