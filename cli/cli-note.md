- [] 希望 ai native 一点
- [] 需要提供好 fs 的 CRUD api 给 timension 和 echo 调用
- [] db 里面需要有 来源

```
   cli/
   ├── cmd/mm/main.go
   ├── commands/
   │   ├── root.go
   │   └── lc/
   │       ├── lc.go
   │       └── init.go         # 调用 workflow/init.Run()
   ├── workflow/               # 应用层：用户场景/工作流编排
   │   └── init/
   │       ├── run.go
   │       └── prompts.go
   ├── config/
   │   ├── config.go
   │   ├── store.go
   │   └── yaml_store.go
   └── infra/
       ├── xdg/
       └── fs/
 ```
