# File Browser 开发笔记

> 本文件由代码走查整理，记录项目整体结构与二次开发注意事项，重点服务于两个待办需求：
> 1. 替换自带的 Markdown 预览（当前效果差）；
> 2. 新增 doc/ppt/excel 预览，尽量支持 Office 在线编辑。
>
> ⚠️ 上游提示：File Browser 已于 2026-09-01 归档，官方不再维护。本仓库是 fork，所有改动需自行承担安全与兼容风险。详见 `README.md` 的 Security 段。

## 1. 技术栈总览

- **后端**：Go（见 `go.mod`，module 为 Go 后端）。负责文件系统访问、鉴权（JWT）、文件类型探测、raw/preview 下载接口。
- **前端**：Vue 3 + TypeScript + Vite 8 + Pinia + vue-router + vue-i18n，包管理用 **pnpm**（`frontend/package.json`，`engines` 要求 node >=24、pnpm >=10）。
- **前后端集成**：前端 `pnpm build` 产物输出到 `frontend/dist`，Go 通过 `embed` 把 `dist/*` 打进二进制（见 `frontend/assets.go`，`//go:embed dist/*`）。**改完前端必须重新 build 才能在后端生效**。
- **构建编排**：根目录 `Taskfile.yml`（go-task）。

## 2. 目录结构（关键部分）

```
filebrowser/
├── main.go               # 入口
├── go.mod / go.sum
├── Taskfile.yml          # 构建任务
├── files/                # 后端文件模型与类型探测（重点 file.go / mime.go）
├── http/                 # HTTP handler、路由、raw/preview 下载接口
├── auth/ users/ share/ settings/ storage/ ...  # 各功能模块
├── docs/                 # 安装/配置/构建文档
└── frontend/
    ├── package.json      # 前端依赖
    ├── vite.config.ts
    ├── assets.go         # go:embed dist/*
    ├── dist/             # 构建产物（被 embed）
    └── src/
        ├── api/          # 前端 API 封装（files.ts 最关键：getDownloadURL/getPreviewURL/getSubtitlesURL/fetch）
        ├── router/index.ts
        ├── stores/       # Pinia（file.ts 存当前文件 req）
        ├── views/
        │   ├── Files.vue             # 路由分发：决定用哪个视图（★见下）
        │   └── files/
        │       ├── FileListing.vue   # 目录列表
        │       ├── Editor.vue        # 文本/Markdown 编辑器（Ace）+ Markdown 预览（★marked）
        │       └── Preview.vue       # 非文本预览（图片/音视频/pdf/epub/csv）（★）
        └── components/files/
            ├── CsvViewer.vue
            ├── ExtendedImage.vue
            └── VideoPlayer.vue
```

## 3. 文件类型是怎么判定的（后端）

`files/file.go` 的 `detectType()`（约 L222-287）给每个文件打上 `Type` 字段，返回给前端：

- 先用扩展名 `mime.TypeByExtension`，拿不到再 `http.DetectContentType` 读文件头；
- 按 MIME 前缀归类为：`video` / `audio` / `image` / `pdf`；
- 文本（`text/*` 或非二进制且 ≤10MB）→ `text`（可改）或 `textImmutable`（只读）；
- 其余 → `blob`（即“无法预览”，前端只给下载按钮）。

> 关键点：**doc/ppt/excel 这类 Office 文件目前全部落到 `blob` 分支**，所以前端没有任何预览，只显示“下载/新窗口打开”。扩展名 `.md/.markdown` 会被判成 `text`。

## 4. 前端预览分发逻辑（★改动核心入口）

`frontend/src/views/Files.vue` 的 `currentView` 计算属性（L65-86）决定渲染哪个组件：

- `isDir` → `FileListing.vue`
- `.csv` → `Preview.vue`（`?edit=true` 时用 `Editor.vue`）
- `type === "text" | "textImmutable"` → `Editor.vue`
- 其它（含 pdf/image/audio/video/epub/**blob**）→ `Preview.vue`

`Preview.vue`（L75-153）内部再按类型细分：epub / csv / image / audio / video / pdf(`<object>`) / blob(仅下载)。

**结论——两个需求各自的落点：**
- **Markdown**：`.md` 走 `text` → `Editor.vue`。Markdown 预览就在 `Editor.vue`：
  - `import { marked } from "marked"` + `marked-katex-extension`（L96-97）；
  - 点“预览”按钮触发 `previewContent = DOMPurify.sanitize(await marked(...))`（L163-173），用 `v-html` 塞进 `.md_preview`（模板 L69-74）。
  - 这就是“效果差”的来源：裸 marked 输出 + 自定义 CSS，无代码高亮、无 GFM 表格/任务列表样式、无目录等。
  - 若要改成“打开 .md 直接渲染”而非藏在编辑器里，需在 `Files.vue` 的 `currentView` 为 markdown 单独分流到一个新预览组件。
- **Office（doc/ppt/excel）**：目前是 `blob`。要支持预览需要：
  1. 让前端能识别这些扩展名（可在 `Preview.vue` 加 `isOffice` 之类的 computed，按 `extension` 判断，不必改后端，因为前端能拿到 `extension` 字段）；
  2. 新增一个 Office 预览组件，拿 `api.getDownloadURL(req, true)` 的 raw URL 去渲染。

## 5. 前端如何拿到文件内容/下载地址

`frontend/src/api/files.ts` 提供：
- `fetch(path)`：拉取 FileInfo（含 `type` / `extension` / `size` / `content`(文本内容) / `path`）；
- `getDownloadURL(req, inline)`：原始文件下载/直链 URL（Office 组件会用它）；
- `getPreviewURL(req, size)`：图片缩略图；
- raw 接口形如 `api/raw<path>`（见 `Preview.vue` epub 用法 L303）。

Pinia 的 `stores/file.ts` 持有当前 `req`，各预览组件通过 `useFileStore().req` 读取。

## 6. 构建 / 运行 / 验证

```bash
# 前端
cd frontend
pnpm install
pnpm dev          # 本地开发（vite dev）
pnpm build        # typecheck + vite build → 产物进 dist/（会被后端 embed）
pnpm typecheck    # vue-tsc 类型检查
pnpm lint         # eslint

# 后端（根目录）
go build ./...    # 或参考 Taskfile.yml / docs
```

**验证改动的最小闭环**：`pnpm build` 成功 → 启动后端 → 浏览器打开对应类型文件确认预览。改前端后记得重新 build，否则 embed 的还是旧 dist。

## 7. 二次开发注意事项

- **安全**：Markdown/HTML 渲染务必做消毒防 XSS。Vditor 自带消毒；若换别的渲染库要补 `DOMPurify.sanitize`。
- **离线/CDN**：`Editor.vue` 的 Ace 与 Vditor 都走 jsDelivr CDN。若部署环境内网隔离，需把这些库资源本地化，并相应调整 `http/http.go` 的 CSP。
- **i18n**：按钮/文案走 `vue-i18n`（`$t(...)`），新增 UI 文本应加到 `src/i18n`。
- **CSP**：网络相关功能受 `http/http.go:contentSecurityPolicy()` 约束。新增外部源（CDN、服务）必须同步放行，否则浏览器直接拦截导致白屏。
- **主题**：深浅色靠 CSS 变量（`var(--fg)` 等）+ `utils/theme.ts`，新组件样式应复用这些变量。
- 本仓库上游已归档，升级依赖/合并上游补丁基本无来源，改动以自维护为主。

## 7.1 已确认的实现方案（2026-10-13）

### Markdown（GitHub 风格,全量打包,有单测）
放弃 Vditor,改用 **markdown-it + highlight.js + github-markdown-css**,全部打包进前端、**零运行时拉取**。
- 渲染逻辑在 `src/utils/markdown.ts`(刻意不碰 DOM,所以能在 Node 里跑单测),导出 `markdownToHtml()`:
  - `markdown-it`:`html:true` + `linkify:true`,GFM 表格/删除线内置,`markdown-it-task-lists` 补任务列表复选框。
  - 代码高亮在渲染时完成:指定语言用 `hljs.highlight`,没指定用 `hljs.highlightAuto`,输出 `<pre class="hljs"><code class="hljs language-x">`。
  - `link_open` 规则把非 `#` 开头的链接加上 `target=_blank` + `rel=noopener`,避免被 SPA 路由吞掉。
- `Editor.vue` 只做三件事:`DOMPurify.sanitize(markdownToHtml(...))` → `v-html`;容器加 `markdown-body` class(github-markdown-css 的选择器);按深浅色动态 `import` 两套样式(`github-markdown-{light,dark}.css` + `highlight.js/styles/github{,-dark}.css`)。
- **单测**:`src/utils/__tests__/markdown.test.ts`,`pnpm test` 验证高亮产出 `hljs-keyword` span、表格、任务列表、链接 target、自动链接。**这是为什么选 markdown-it 的关键原因**——纯 JS 可在 Node 里断言,不用靠浏览器瞎猜。
- 历史教训(踩了三轮):Vditor 的高亮/解析器是**运行时懒加载**的,排查链路长且只能在浏览器里看。期间踩过:
  1. 主题名写了 `highlight.js/styles` 里不存在的 `native` → CSS 404,深色完全无高亮;
  2. 自托管时把 URL 放到 `/vditor/...`,但 Go **只在 `/static` 前缀伺服内嵌文件**,其它路径落到 SPA 兜底、**返回 index.html 且状态码 200** → Vditor 把 HTML 当 JS 执行,预览全白;
  3. `static.go` 对 `.js` 请求**只读同名 `.js.gz`**,`public/` 手放的文件没有 .gz 会 404。
- ⚠️ 通用教训:**验证静态资源别只看状态码**,要看 `content_type`/`size`,并用一个不存在的路径做反例(应 404)。
- 链接可点:`Vditor.preview` 的 `after()` 回调里把所有 `a[href]`(非 `#` 锚点)设成 `target=_blank` + `rel=noopener`,避免被 SPA 路由吞掉。
- 改动文件：`utils/markdown.ts`、`utils/__tests__/markdown.test.ts`、`Editor.vue`、`css/mdPreview.css`、`package.json`、`env.d.ts`。Vditor 及其自托管脚本/资源已全部移除。

### Office（当前方案:服务端转 PDF 预览,只读）
需求定稿:**只做预览不做编辑,且远程访问要快**。因此放弃"把源文件下到浏览器再渲染"(大 pptx 传输+JS 解析都慢),改为**服务端用 LibreOffice 转 PDF**,浏览器只收体积小得多的 PDF(实测 20MB pptx → 1.6MB PDF),用原生 PDF 查看器渐进加载。
- 前端:`Preview.vue` 按扩展名(`isOffice`)分流到 `components/files/OfficeViewer.vue`;组件请求 `getOfficePreviewURL()` 拿到 PDF(blob)后用 `<object type=application/pdf>` 显示,转换期间显示 loading。
- `api/files.ts:getOfficePreviewURL()`:带 `X-Auth` 请求 `/api/office/preview{path}`,拿 blob 转 objectURL。
  - ⚠️ **踩过的坑**:这里传的是 `fileStore.req.path`(已是用户根下的资源路径),**不能再过 `removePrefix()`**。`removePrefix` 是给 `/files/xxx` 这种**路由**路径用的(砍掉前两段),误用会把 `/person/a.docx` 砍成 `a.docx`,后端 404 `lstat /srv/a.docx`。路径用 `encodePath()` 编码。
- 后端 `http/office.go`:`GET /api/office/preview{path}`(`withUser`,需 Download 权限)
  - 用 `soffice --headless --convert-to pdf` 转换(每次用独立临时 profile,`-env:UserInstallation`)。
  - **按 path+mtime+size 缓存**到 `os.TempDir()/fb-office-pdf`,同一文件只转一次;重复打开直接给缓存,秒开。
  - 并发用 `officeConvertSem`(容量 1)限流,保护内存;单次转换 120s 超时。
  - 支持现代格式**和旧版二进制 .doc/.xls/.ppt**(soffice 都能转)。
  - 没装 soffice 时返回 501,前端提示不可用。
- 运行依赖:容器内必须有 `soffice`。镜像 `Dockerfile.office`(Ubuntu 22.04 + libreoffice-impress/calc/writer + **fonts-noto-cjk**(中文!))。转换只在打开未缓存文件时短暂吃内存,无常驻服务。

> **OnlyOffice 在线编辑已按需求删除**:`http/onlyoffice.go` 及其路由、前端调用、CSP 中的相关放行全部移除。只保留 Office 只读预览。

### 部署镜像要点（Dockerfile.office）
- `FROM docker.m.daocloud.io/library/ubuntu:22.04`(走 daocloud 镜像源,home 能拉)。
- apt 装 libreoffice 三件套 + `fonts-noto-cjk`(否则中文 PDF 是方框)+ `libcap2-bin`+`curl`+`ca-certificates`。
- 非 root 用户 uid/gid 1000(匹配 host 的 `/data/filebrowser/storage` 属主);`setcap cap_net_bind_service` 让非 root 绑 80。
- `entrypoint.sh`:首次没有 `/config/settings.json` 就从 `/defaults` 拷一份,然后 `filebrowser -c /config/settings.json`。
- 挂载沿用原 `fb` 容器:`/config`、`/database`、`/srv`,端口 `8088->80`,`--restart always`,`-e PUID/PGID=1000`。镜像约 1.5GB(home 根分区余量够)。

## 8. 关键文件清单

- `frontend/src/views/Files.vue:65`（视图分发：目录/CSV/文本→Editor，其余→Preview）
- `frontend/src/views/files/Editor.vue`（Markdown：`markdownToHtml` + DOMPurify + `markdown-body`,主题样式按深浅色动态 import）
- `frontend/src/utils/markdown.ts`（markdown-it + highlight.js 渲染管线,有单测）
- `frontend/src/utils/__tests__/markdown.test.ts`（`pnpm test` 验证高亮/表格/任务列表/链接）
- `frontend/src/views/files/Preview.vue`（非文本预览分发；`isOffice` 分流到 `OfficeViewer`）
- `frontend/src/components/files/OfficeViewer.vue`（Office→PDF 预览,`<object>` 显示）
- `frontend/src/api/files.ts`（`getOfficePreviewURL`）
- `http/office.go`（Office→PDF 转换+缓存+限流,`/api/office/preview`）
- `http/http.go`（路由注册 + `contentSecurityPolicy()`）
- `files/file.go:222`（后端类型探测，Office 落 `blob`，前端按扩展名识别）
- `frontend/package.json` / `frontend/assets.go`（依赖 / dist embed）
- 部署构建上下文(本机 `/tmp/fb-deploy`,home `~/fb-deploy`):`Dockerfile.office`、`entrypoint.sh`、交叉编译的 `filebrowser` 二进制


