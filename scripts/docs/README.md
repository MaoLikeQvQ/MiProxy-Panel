# 文档截图

使用演示数据生成 README 的三张截图；不读取用户订阅或安装内核。Web 数据由浏览器拦截返回，终端图来自临时目录中实际程序的 PTY 输出，使用浏览器终端渲染器还原颜色和布局。

先构建前端并启动静态预览，再执行：

~~~sh
python3 -m http.server 18788 --bind 127.0.0.1 --directory web/dist
# 另一个终端；Playwright 由开发环境提供，不改项目运行依赖
PLAYWRIGHT_MODULE=/绝对路径/playwright/index.mjs \
MIPROXY_SCREENSHOT_BINARY=/绝对路径/miproxy-panel \
node scripts/docs/screenshots.mjs
~~~

输出为 docs/screenshots/web-overview.png、web-rules.png、terminal.png。终端捕获脚本启动临时面板，导入无真实节点的演示 YAML，完成后关闭子进程并删除临时配置。内核控制接口固定为不使用的 127.0.0.1:1，不接入已有内核。脚本复用同一开发环境 Playwright 内置的 xterm 渲染资源；升级该工具时需核对资源路径。
