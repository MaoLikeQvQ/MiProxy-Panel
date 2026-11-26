# 项目工作约定

- 命令入口在 cmd/miproxy-panel；业务归 internal。server 与 terminal 不互相导入，共享契约放 api，YAML 合并放 profile。新逻辑与测试跟随所属包。
- 前端按 features 组织，公共能力放 shared；表单状态与校验不要重新堆回 App.vue。沿用主题变量、表单组件、桌面与手机布局。
- 运行配置、订阅和认证记录是用户数据；结构整理不得移动、删除或覆盖这些文件。认证不使用 PANEL_PASSWORD / MIHOMO_SECRET，内核通过扫描及配置取得密钥。
- make build 构建嵌入 Web 的本机程序；make test 运行 Go 竞态与前端单元测试；make check 检查类型、Go vet 和脚本。浏览器测试先启动 18788 预览，再 make test-browser。
- 构建入口始终为 ./cmd/miproxy-panel，版本注入 internal/buildinfo.Version。路径变化同步 Makefile、Dockerfile、CI、scripts 与 docs。
- Linux 安装测试只在可丢弃的隔离 Linux 中执行 tests/integration/linux_install.py，不在宿主系统安装测试服务。
- 发布说明、用法、环境选包和体积测量均在 docs；文档截图只使用独立演示数据。体积需测实际产物并注明是否含内核和 Geo 数据。
- 保留根目录 install.sh 与现有产物名称，避免破坏用户安装命令。发布新版本不覆盖已发布标签。
