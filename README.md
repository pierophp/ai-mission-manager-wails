# AI Mission Manager

Aplicativo desktop com Go + Wails v3, React, TypeScript e Vite+ (`vp`), uma toolchain unificada que reúne Vite, Rolldown, Vitest, Oxlint, Oxfmt e Vite Task. O projeto está configurado para desktop (macOS, Windows e Linux); não inclui alvos nem tarefas de build mobile.

## Requisitos

- Go 1.25 ou superior
- Node.js e npm (o `vite-plus` local disponibiliza a CLI `vp`; não exige instalação global)
- Wails CLI v3 (`go install github.com/wailsapp/wails/v3/cmd/wails3@latest`)
- Dependências nativas da plataforma: veja [instalação do Wails](https://v3.wails.io/quick-start/installation/)
- `git` e `tmux` no `PATH` para operações com repositórios e Runs
- CLIs dos providers que você usa: `gh` para GitHub, `twg` para Atlassian e `az` para Azure DevOps
- `claude` e/ou `codex` no `PATH` para iniciar Runs com esses agentes

O setup e a tela de saúde mostram quais dependências estão disponíveis. Instale apenas os CLIs dos providers e agentes que pretende usar.

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

No macOS, instale as ferramentas nativas exigidas pelo Wails e então execute `wails3 build`; o binário é gerado em `bin/`.

## Terminal externo no Linux

A ação de abrir um Run no terminal externo conecta ao mesmo Pane do `tmux`, inclusive quando a Machine usa SSH. No macOS ela continua abrindo o Terminal via AppleScript.

No Linux, o app procura primeiro [`xdg-terminal-exec`](https://github.com/Vladimir-csp/xdg-terminal-exec), que usa o terminal configurado no desktop. Se ele não estiver instalado, prioriza Konsole no KDE, GNOME Terminal no GNOME e Xfce Terminal no Xfce; depois procura Kitty, Alacritty, GNOME Terminal, Konsole, Xfce Terminal, Foot (somente em Wayland) e Xterm no `PATH`.

Para escolher explicitamente um desses emuladores, defina `AI_MISSION_MANAGER_TERMINAL=kitty` no ambiente usado para iniciar o app. A variável aceita o nome ou caminho do executável, sem argumentos. Para outros emuladores ou configurações personalizadas, configure o `xdg-terminal-exec`. Se nenhum emulador compatível estiver disponível, a ação retorna um erro com as opções de instalação.

## Banco de dados e migração do Tauri

O Wails usa o mesmo banco do app Tauri em `~/.ai-mission-manager/mission-manager.sqlite`. Execute apenas uma instância do Mission Manager por vez: nunca deixe os apps Tauri e Wails abertos simultaneamente sobre esse banco. O app Wails permite apenas uma instância aberta por vez.

Se o banco ainda precisar de uma migração de schema, abra-o pelo app Tauri primeiro e feche o Tauri antes de iniciar o Wails. O Wails recusa schemas legados que não reconhece; ele não executa as migrações antigas do Tauri.
