# JoBot

Aplicativo desktop para Windows que busca vagas em vários sites, pontua cada uma
contra o seu currículo e mostra o que falta antes de você se candidatar.

![Resultados de busca no JoBot](docs/screenshots/search-results.png)

## Download

**[Baixar JoBot v1 para Windows](../../releases/latest)** — `JoBot.Setup.1.0.0.exe`, 615 MB.

O instalador é grande porque já vem com tudo embutido: o runtime Python e o
navegador usado para abrir as páginas de vaga. Não precisa instalar mais nada.

> **Aviso do Windows:** o instalador não é assinado digitalmente. O SmartScreen vai
> mostrar "Windows protegeu o computador". Clique em **Mais informações** →
> **Executar assim mesmo**.

Para conferir o arquivo antes de instalar:

```powershell
Get-FileHash "JoBot.Setup.1.0.0.exe" -Algorithm SHA256
```

```text
60E2CB1AD529FBB328C0F683A2EDA687CE12C257E0B0E47499160996A8CF1A84
```

Requisitos: Windows 10 ou 11, 64 bits. Instala por usuário, sem pedir admin.

## O que ele faz

- **Busca em várias fontes** — LinkedIn, Indeed, Gupy, RemoteOK, Remotive,
  WeWorkRemotely, Arbeitnow e Jobicy.
- **Pontua cada vaga** contra o seu currículo e ordena por compatibilidade,
  separando o que vale revisar do que ficou abaixo do corte.
- **Mostra o porquê da nota** — quais requisitos da vaga não têm evidência no seu
  currículo, em vez de só cuspir um número.
- **Resume Studio** — diagnóstico do currículo, ajuste para uma vaga específica e
  export em PDF. O diagnóstico e o export funcionam offline, sem chave de IA.
- **Vagas salvas, candidaturas e histórico** ficam no banco local.

## Privacidade

- Todos os dados ficam no seu computador, em `%APPDATA%\Sencia Job` (SQLite).
- A chave de API que você configurar é cifrada com **DPAPI** do Windows, presa à
  sua conta de usuário.
- **Sem telemetria e sem auto-update.** O app só faz requisições para os sites de
  vagas e, se você configurar uma chave, para o provedor de IA que você escolheu.
- A IA é opcional. Sem chave configurada, a busca, a pontuação offline, o
  diagnóstico do currículo e o export em PDF continuam funcionando.

## Telas

| Resume Studio | Provedores de IA |
| --- | --- |
| ![Resume Studio](docs/screenshots/resume-studio.png) | ![Configuração de provedores de IA](docs/screenshots/settings-ai.png) |

Tema claro também:

![Tema claro](docs/screenshots/search-results-light.png)

## Sobre

Projeto pessoal. O código-fonte não é público — este repositório distribui só o
instalador e a documentação.

As telas usam dados de demonstração.
