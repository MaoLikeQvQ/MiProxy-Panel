# 工程结构与依赖

## 目录职责

~~~text
cmd/miproxy-panel/      可执行入口，只处理退出码
internal/
  app/                 serve / tui / access 命令分发
  server/              HTTP、私有 Socket、Mihomo 与持久化协调
  terminal/            终端状态、视图、客户端、服务与更新操作
  api/                 服务与终端共用的 JSON 契约
  profile/             YAML 订阅合并、覆写与规则归组
  access/              应用内认证与本机初始化
  fileutil/            原子写入与有上限的普通文件读取
  download/            下载客户端与系统代理
  runtimeenv/          非认证进程配置和 Socket 路径
  safeoutput/          终端安全文本与 URL 脱敏
  buildinfo/           构建版本号
web/
  assets.go            Web 编译产物嵌入
  src/app/             导航与配置应用协调
  src/features/        按内核、概览、规则等业务功能组织
  src/shared/          请求、格式、主题与基础组件
  src/styles/          布局与主题变量
  tests/unit/          配置行为单元测试
  tests/browser/       各正常入口的浏览器回归
scripts/release/       发布构建与体积测量
scripts/docs/          可复现文档截图
tests/integration/    可丢弃 Linux 系统安装验收
deploy/                安装脚本、服务与配置模板
docs/                  使用、选包、架构、截图及验证记录
~~~

## 依赖方向

cmd → app → server / terminal。服务与终端可以依赖 api、profile、fileutil 等内部模块，但不互相导入。终端通过 HTTP 契约和私有 Unix Socket 管理后台，不访问后台私有状态。配置合并属于 profile，不能放回终端。api 只包含传输数据，不包含锁、进程或密钥扫描状态。

服务端当前保留一个 server 包，在事务锁、连接快照和配置保存之间保持一致；文件按 config、core、discovery、subscriptions、rules、history 等职责组织。后续拆包应先确定数据所有权和操作接口，避免为了目录层级公开内部字段。

前端 App 负责页面导航和保存应用协调，表单属于对应 feature。RuleEditor 管理表单和反馈，ruleForm 产生结构化提交，App 执行 YAML 更新、回滚和重载。公共函数不从业务视图反向导出，例如字节格式化归 shared。

## 测试与构建

Go 单元测试跟随所属包；跨系统安装测试放 tests/integration。浏览器回归使用模拟 API，真实服务和内核验收单独记录。

根目录 make build、make test、make check 是日常入口。浏览器测试先启动 18788 静态预览或指定 MIPROXY_TEST_URL，再执行 make test-browser。正式构建入口为 scripts/release/build.sh；版本注入到 internal/buildinfo.Version。Docker、CI 和打包都使用 ./cmd/miproxy-panel。

运行数据继续放配置目录。bin、artifacts、dist-release、web/dist 和 node_modules 为忽略产物，不与源码混交。根目录保留 install.sh、Dockerfile、compose.yaml 以保持部署入口稳定。此次整理不移动用户配置、内核或已发布文件。

## 变更约定

实现与测试归所属业务模块；有真实多个使用方再提取共享模块。路径变动同步 Makefile、Docker、CI、文档和测试。响应结构变动检查 Web 与终端。认证材料不放回环境模板，截图只使用独立演示数据。
