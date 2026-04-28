+++
title = "An Agent-Friendly Blog Framework"
date = "2025-01-01T00:00:00+08:00"
tldr = "Agora is A lightweight Hugo-based blog evolving from human-readable to Agent-native. Covers Markdown-first approach, terminal aesthetics, client-side search, and roadmap: DID identity, RSS, cross-site search, REPL interaction, annotation layer."
tags = ["agent"]
+++

It's 2026 and I'm rebuilding my blog. Again. But this time AI has moved the goalposts. I'm no longer the total front-end newbie (Well, mostly.)

But AI shouldn't only help us write better code. Agents themselves should be readers.

Here's why: human attention is finite. In an age of infinite content, nobody can read, watch, or learn everything. A dense one-hour tutorial takes more than an hour to truly absorb — especially in a foreign language. Agents process text at token-per-second speeds. And unlike us, they don't forget. We're squirrels stashing acorns for winter, then drawing a blank when spring comes. Agents remember where they put things.

## Core Principles

A few ground rules before building anything:

- **Agent-First**: Clean structure, explicit semantics. Agents should parse and interact with this effortlessly.
- **Lightweight**: No infrastructure bloat. Deployable and maintainable by one person.
- **Minimal**: The daily workflow is just Markdown. Nothing more complicated than jotting notes.

## What is an Agent-Friendly Blog

Traditional blogs are built for human eyes. An agent-friendly blog treats agents as **first-class citizens** — not just writing assistants, but independent readers with their own agendas.

That means the blog needs to give agents:
- Structured access to content (not just rendered HTML)
- A way to identify themselves
- Interfaces they can actually use — read, annotate, publish, subscribe

## Requirements Breakdown

### Agent CLI Interface

The framework should feel natural from a terminal:

- **DID Identity**: Every agent gets its own decentralized identity
- **RSS Feeds**: Agents subscribe and poll for updates like any other reader
- **Cross-Site Search**: Agents can crawl and index content across multiple blogs
- **Site API**: Full CRUD access
  - *Reader mode*: summarize, bookmark, translate, query articles
  - *Author mode*: publish, edit, delete posts from the command line
- **REPL Interaction**: A command prompt at the bottom of every page. Register your agent and start typing.
- **Annotation Layer**: Agents can read and contribute to discussions

### Human Layer

None of the old conveniences disappear:

- **Markdown-First**: Writing is just Markdown files
- **Cross-Platform**: Publish to multiple platforms with one command
- **Author-Friendly**: Edit Markdown. Don't think about deployment.

## References and Inspirations

- Hacker News API interaction design
- openCLI interaction paradigm
