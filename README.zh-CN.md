# tui-dashboard

[English](README.md)

配置驱动的终端看板,基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea)。

单个 YAML 文件定义全部内容:**`sources`**(按 interval 周期执行的 shell 命令)和
**`layout`**(一排排展示这些输出的 widget)。

每个源声明七种类型之一 ——
`text` `number` `array` `timeseries` `map` `table` `logs` ——
该声明决定脚本的输出格式与接受该类型的 widget。

widget 的标题、标签、format 与静态文本都是 Go 模板;内置五套配色主题。

仓库内附示例配置 [example.zh-CN.yaml](internal/config/example.zh-CN.yaml)。

![tui-dashboard](screenshot/1.png)

## 用法

需要 Go 1.25+。

```bash
chmod +x scripts/*.sh                  # 仓库内首次

go run .                               # 自动查找配置文件(见下)
go run . --config path/to/config.yaml  # 使用该文件
go run . --init                        # 把内置示例及其脚本复制到默认配置路径
go run . -v                            # 同时输出启动期信息(配置来源、工作目录、提示)
```

按键:`?` 帮助 · `r` 重载配置 · `q` / `Ctrl+C` 退出 · `↑`/`k` `↓`/`j` 滚一行 · `PgUp`/`PgDn` 翻页 ·
`Ctrl+B`/`Ctrl+F` 整页 · `Ctrl+D`/`Ctrl+U` 半页 · `Home`/`End` 到顶/底 · 鼠标滚轮。

### 安装为命令

```bash
go build -o "$(go env GOPATH)/bin/tuidash" .
```

## 配置文件

顶层四个键:

| 键 | 含义 |
|-----|------|
| `theme` | `default` \| `dracula` \| `gruvbox` \| `nord` \| `light`;空 = `default` |
| `poll_interval` | UI 重渲染频率(默认 `500ms`),与数据源的 interval 无关 |
| `sources` | 数据源 |
| `layout` | 一排排的 widget |

配置文件的查找顺序,取第一个存在的:

1. `--config <路径>` —— 就用这个文件;
2. 当前目录的 `./config.yaml`;
3. `$XDG_CONFIG_HOME/tui-dashboard/config.yaml`(通常为 `~/.config/tui-dashboard/config.yaml`);
4. 都没有 —— 内置的 `internal/config/example.yaml`。

`go run . --init` 把内置示例与 `scripts/` 复制到默认配置路径并打印路径。已存在的文件不
覆盖,只补齐缺失的脚本。

## 配置项

配置分两步:

1. **声明数据** —— `sources` 列出要执行的命令,以及每条命令产出哪一种数据;
2. **排布界面** —— `layout` 把 widget 排成一行行,每个 widget 就是一行。

### `sources`

```yaml
sources:
  - name: cpu                  # 被 widget 引用
    type: map                  # 必填:text|number|array|timeseries|map|table|logs
    cmd: ./scripts/sys_cpu.sh  # 必填:任意 shell 命令(经 sh -c 执行)
    interval: 2s               # 执行周期
    timeout: 3s                # 缺省 min(interval, 5s)
    history_cap: 80            # 图表序列长度(默认 60)
    log_cap: 200               # 日志缓冲容量(默认 200)
    header: true               # 仅 type: table —— 首行是不是表头(默认 true)
```

每种类型只有一种输出格式:

| `type` | 脚本输出什么 | 解析后的值 | 跨帧怎么处理 |
|--------|-------------|-----------|-------------|
| `text` | 任意文本 | 一个字符串 | 原样保留,从不解析 |
| `number` | 整段就是一个数 | 一个数 | 每帧记一个采样点(程序打时间戳) |
| `array` | 每行一个数 | 一串数,不带时间戳 | 不累积 —— x 轴是序号 |
| `timeseries` | 每行 `<时间戳> <数值>` | 一串数 | 按时间戳累积(脚本打时间戳) |
| `map` | 每行 `键: 值` | 一个具名对象 | 数值字段各自累积;`value:` 指定取哪个 |
| `table` | 每行一条记录(首行可表头) | 若干行 + 一份列序 | 不累积 |
| `logs` | 每行一条日志(时间/级别可省) | 若干行,各带时间与级别 | 追加进滚动缓冲 |

"解析后的值"就是模板看到的内容:`map` 源是一个对象,故 `{{.mem.used_pct}}` 可以取字段;
`number` 源就是这个数,故 `{{.load_num}}` 直接是值;`text` 与 `logs` 源是一行字符串。
时间戳保留在源内部 —— 模板拿到的是一串数,时间轴由渲染层处理。

无论声明成哪种类型,原文都一并保留,`text` widget 因此可以显示脚本的原始输出。输出不属于
上表任何一种形状时,需在**脚本里**转换。

### `layout`

`layout` 是一串行。只有 `title`、没有 `widgets` 的行是一条独立的标题带 —— 示例最上面
那条大标题就是这样一行。一行里的 widget 横向并排。

自底向上最后一行含 `logs` 面板时,该行会吸收屏幕剩余高度、露出更多日志。给它写一个
`height:` 就改成定高(示例里日志那一行就是这么收成 10 行的)。

widget 写 `width:` 就按这个宽度定死(含边框),不写的平分剩余宽度。整行定宽之和超出终端
宽度时按比例一起缩,故窄终端上是列被截断,而不是整行挤坏。

```yaml
layout:
  - title: 'System Monitor {{now "01-02 15:04:05"}} {{.weekday}}'
    title_align: center
    color: '#f8f8f2'
    bg: '#44475a'            # 把整条标题行铺成一条色带
  - title: Overview
    widgets:
      - type: stat
        source: cpu
        value: '{{.cpu}}'    # 模板;源声明的类型已经决定取什么时可以整体省略
        format: "%.0f%%"     # Sprintf;本身也可以是一段模板
        title: CPU
        title_align: right   # left|center|right(默认 center)
```

行字段:`title` `title_align` `color` `bg` `height` `widgets`。

widget 字段:`type` `source` `value` `title` `title_align` `color` `style` `format` `label`
`min` `max` `x_format` `y_format` `labels` `columns` `max_lines` `time_format` `text` `wrap`
`width` `height`。

### widget

| `type` | 显示什么 | 可接受的源类型 |
|--------|---------|-----------------|
| `stat` | 一个数,上方可带 `label` | `number` `array` `timeseries` `map` |
| `gauge` | 一个数,画成 `min`–`max` 之间的条 | `number` `array` `timeseries` `map` |
| `chart` | 一条序列,画成折线(`style`:`braille` \| `dots` \| `line`) | `number` `array` `timeseries` `map` |
| `bar` | 一条序列,画成条形(`style`:`hbar` \| `solid` \| `vbar`) | `number` `array` `timeseries` `map` |
| `heatmap` | 一年的逐日数值,每天一格 | `timeseries` |
| `table` | 记录表(`columns` 定列) | `table` |
| `logs` | 滚动日志缓冲(深 `log_cap` 行) | `logs` |
| `text` | 该源的原始输出,或静态 `text:` 字符串 | 全部类型 |

widget 渲染的是上表中"解析后的值"经 `value:` 选出来的那一份,不是脚本的原始输出。
`stat` 与 `gauge` 取所给值的最后一个,`chart` 与 `bar` 画整条序列,两者读的是同一份值,
因此同一个源可供多个面板使用。`table` 与 `logs` 各只渲染一种形状,只接受一种源类型;
`text` 接受全部类型。

`heatmap` 画滚动的最近 52 周,每个本地日历日一格:周从左到右、星期从上到下,晚于今天的格子
留空;落在同一天的点求和,故脚本每天输出一个点即可。`max:` 定满格对应的数值(默认取窗口中
最大的一天),档位只靠颜色区分。

它只接受 `timeseries` 源,`history_cap` 需调到 370 左右才够画满一年(默认的 60 只能填满最近
两个月);网格占 112 列,因此要独占一行。

### `value:`

`value:` 是一段模板,从源里选出要的内容(`'{{.used_pct}}'`)。**源声明的类型已经决定取
什么时,整个 `value:` 可以省略** —— `number`、`array`、`timeseries`、`table`、`logs` 都是
这样。只有 `map` 不能省略:一个对象有若干字段,不指定字段名是有歧义。

### 模板

widget 的标题、正文、`format`、`label`、静态 `text`、列标题都是 Go 模板。可读内置的
`.date` / `.weekday` / `.time`,每个数据源按源名暴露的结果(形状由该源声明的 `type:`
决定 —— `map` 源写 `{{.mem.used_pct}}` 取字段、`number` 源写 `{{.load_num}}` 直接就是
数、`text` 与 `logs` 源是一行字符串),以及该 widget 自身源的字段平铺到顶层。

行标题是例外:它只看得到内置那三个字段,写在行标题里的 `{{.host}}` 取不到值。实时数据
应放入该行的 widget。

源名同时是模板里的字段名,故**不要用连字符**:`{{.disk_mounts}}` 可以取到那个源,
`{{.disk-mounts}}` 是模板语法错误(Go 模板的字段名不吃 `-`)。
