# 红果鉴 / 真果鉴

> ⚠️ **免责声明**：本项目源码来自网上大名鼎鼎的**鱼佬**（原作者）。我只是把它拿来打包、测试着玩，方便自己用，**不保证任何可用性，随时可能删库**。

Flutter 多端独立短剧 / 影视应用，原名「短剧库 APP」。站源请求、解析、下载和播放均在设备上完成，不依赖自建服务。当前源码版本：**0.2.68+75**。

这个仓库是我自己用的分支，在上游基础上修了一批站源和播放问题，顺手也把一些体验补上。下面是这个分支相对上游的主要变化，以及使用、构建和站源开发说明。

## 这个分支改了什么

### 0.2.68+75

- **升级需要重装**：Android 发布签名换成了新密钥，旧版本无法覆盖安装，请先卸载（会清数据，建议先在旧版导出备份）再装新包。Windows 端不受影响。
- 更新遇到超时、429、408、5xx 会一直自动退避重试，**连续失败累计满 60 分钟**才暂停，并保留「接着更新」入口；中途只要有一次请求成功，计时就重新开始。
- 修复 TV 上选集弹窗的自动定位：以前打开弹窗后按方向键会跳到错误的集，或头几百毫秒完全没反应。
- 站点凭据收紧为**同源附带**：镜像域或跨域请求不再带上原域的 Cookie / UA / 令牌，黄豆的授权 POST 被跨域重定向时直接拒绝。

### 0.2.66 ~ 0.2.67

- **一键更新**：一个按钮同时更新所有已启用的站源，按钮始终是「一键更新」，可反复点——只处理上次失败或还没更新完的源，已成功的会跳过。
- **资料补齐提速**：「网络与资源」里新增「加快资料补齐」预设（并发 6 / 间隔 100ms），点击后保存。改成并发抓取 + 批量落盘，合成测试里黄果视频形态约快 6.8 倍、红果类来源约快 9.6 倍（合成数据，不代表真实站源的网络速度）。
- **黄果 AI 选集修正**：修正部分剧集第 1 集丢失、集号整体 +1 的问题，集号统一为从 1 开始的连续编号。
- **野果容错**：源站 `sort` 字段重复或乱序时不再导致整个站源更新失败；`sort` 正常时保留源站原始集号，避免影响观看记录与已下载文件。
- 影片卡片右下角显示所属站源名（支持你自己改的站源名）。
- 播放页宽屏右侧面板新增「选集 / 简介」切换。
- 目录翻页到底不再报错：正片目录翻到越界页时按「目录已加载完」收尾，并且**每轮更新都会重新探测尾部页**，新上架的剧能被发现。
- 修复站源管理在 release 版下偶发的界面崩溃。

### 0.2.65

- 站源管理支持增删改查、改名、单独启停，并支持导出 / 导入站源更新结果（换设备不用重新跑）。
- 「更新」改为一次点击持续跑到任务完成：先查新剧，再连续加载历史分页，最后分批补齐剧集资料。
- 站点要求人机验证时，可在 Android 内嵌验证页完成并回填本人会话（Windows 需用外部浏览器验证后手工粘贴）。
- 搜索「全部站源」时先渲染已返回的结果，顶部显示细进度条，不再长时间停在「正在加载剧集」。
- 点击卡片立即进入播放页并去重，重复点击不会开出多个播放界面。
- 修正黄果站源把详情页「相关推荐」卡片误当选集导致的集数错乱。

### 网络相关

- 「设置与备份 → 网络与资源」新增管理员开关「**允许本机代理替换证书（关闭 TLS 校验）**」，默认关闭。用 Clash / v2ray 之类本机代理并且它替换了证书时，打开可让部分站源连通。
- 证书类错误会直接说明原因和两条解决办法。
- 新增「**疑似本机代理拦截**」识别：某个域名被代理瞬时拦下（快速返回空响应、没有 Server 头）时会明确提示是代理拦截，建议改用直连或换节点，而不是笼统报一个 HTTP 404。
- 站源多域名失败时，报错会同时显示**主域名**的原因，不再只显示最后一个域名。

## 两个编译版本

| 编译方式 | 应用名称 | 可用站源 |
| --- | --- | --- |
| 默认（不加参数） | 红果鉴 | 仅红果 |
| 加 `--all-sources` | 真果鉴 | 红果、韩小圈、鬼片、青空，以及黄豆、剧果、野果、帝果、黄果视频、黄果 AI、黄果旧版 |

默认可见站源（红果、韩小圈、鬼片、青空）无需密码；其余站源默认隐藏，在站源管理里通过密码锁解锁后显示。已有受限用户不会自动获得新增站源权限，管理员可在用户管理中勾选。

这是**编译选项**，应用内不能切换版本。标题、Android 桌面名称与电视横幅、Windows 窗口与分发文件名、iOS 显示名随编译选项变化。界面、站源调用、原生下载调度同时限制可用站源；红果版不会访问或继续执行其他站源的旧任务。

两版保留原 Android / iOS 应用标识及数据目录；使用同一签名时可相互覆盖升级，但不能作为两个独立正式应用并排安装。切换版本保留追剧、观看记录、用户权限和下载记录；备份格式保持兼容。

## 使用

| 功能 | 操作 |
| --- | --- |
| 浏览 | 标题栏依次提供排序与筛选、榜单、多选下载和展开搜索。分类保持独立一行，内容区左右滑动切类；支持默认、名称、自然季号、上线日期、热度、播放量排序及连载状态筛选。列表下滑接近底部自动加载下一页，底部「加载更多」为兜底 |
| 搜索 | 红果合并官网与名称索引，输入停顿 300 毫秒显示最多 10 条联想；韩小圈、剧果、野果、鬼片调用各自的在线搜索并按分页继续加载。部分失败保留有效结果；最近 20 次搜索按用户保存。选择「全部站源」时哪个站源先返回就先渲染，顶部保留细进度条 |
| 推荐 | 仅红果分类栏在「全部」后显示「推荐」，可切换真人剧、漫剧、AI 剧并分别记住位置 |
| 详情 | 普通点剧立即进入播放页，详情拉取在播放页内完成并带去重锁；下载和兜底入口保留详情页。详情页浏览封面、资料、追剧状态与可展开简介；底部固定「立即播放 / 继续播放」和下载入口。选集默认折叠，展开后每组 50 集，可切换范围、定位当前或输入集数跳转；宽屏与电视采用资料、选集分栏 |
| 追剧与历史 | 按想看 / 在看 / 已看 / 有更新筛选；红果已追剧发现新季后进入「有更新」。追剧和历史均可搜索。每个用户独立保存，每 5 秒及退出播放时记录真实进度 |
| 卡片与批量下载 | 卡片「更多」提供收藏、状态和下载选集；电脑右键，电视菜单键。发现页长按卡片或点标题栏「多选下载」，一次最多 50 部 |
| VIP | 全站源版浏览黄豆或全部站源时显示 VIP 图标，仅过滤黄豆；默认隐藏已确认的 VIP。未知、免费、VIP 分别保存，未知不会覆盖已知状态 |
| 手机播放 | 上滑下一集、下滑上一集；长按 350 毫秒临时 3 倍速。竖屏轻点播放 / 暂停，横屏轻点控制条，双击播放 / 暂停。视频下方为「选集 / 简介 / 下载」Tab |
| 画中画 | Android 手机 / 平板播放器接入画中画按钮；进入前隐藏自绘控制层与弹幕，小窗只保留画面。Android TV、Windows、iOS 暂未接入 |
| 画质增强 | 仅 Windows 桌面端显示并运行增强链路（关闭 / 自动 / 省电增强 / 清晰优先）；移动端与电视端隐藏 |
| 弹幕 | 红果在线播放默认开启；播放器侧边圆形「弹」字按钮可关闭、查看状态及失败重试。本地播放不加载弹幕 |
| 下载 | 播放页「下载」Tab 和详情页下载入口均可选择分集与画质；支持批量暂停、继续、重试、删除及清理任务但保留视频。「更新本剧」补新增或缺失分集 |
| 站源管理 | 管理员可添加（恢复已移除的内置站源）、移除、编辑显示名称，并用开关单独启用或停用某个站源；停用后浏览、搜索、播放与更新全部暂停，但卡片保留以便重新启用。每张卡片显示缓存数量、最近更新时间、分页位置、运行阶段与健康检测详情，可复制诊断信息。受限站源仍需先解锁站源密码，才能出现在「添加」菜单里。「验证与授权」可查看站点地址并导入本人会话：Android 内嵌验证页完成人机验证后自动回填同域 Cookie 与 User-Agent，Windows 在浏览器中验证后手工粘贴，黄豆另可导入本人站点授权令牌；会话仅保存在本次运行内 |
| 站源更新 | 单源点一次「更新」即持续跑到底。请求超时、限流 429 / 408、5xx 自动退避续跑，连续失败 60 分钟后暂停并保留「接着更新」；连接被拒等硬网络错误连续重试后中断；弹验证、认证失败、磁盘保存失败立即中断。中断均保留进度：保存类中断提供「重试保存」，已取得目录进度时出现「接着更新」从缓存位置续跑（尚未取得任何一页时显示「更新」，点击即重新请求第 1 页并继续），「停止」可随时取消。标题栏「一键更新」则一次性并发更新所有已启用的站源 |
| 站源结果导入导出 | 管理员可在站源管理标题栏「导出站源结果」把已更新的目录、分页位置与更新进度保存为 JSON（默认名 `zhenguojian-source-results-时间.json`），在另一台设备用「导入站源结果」合并；同名站源进度更靠前时保留本机进度，导入后点「更新」或「接着更新」即可从导入的分页位置续跑。文件不含站点会话凭据、下载任务、追剧记录与代理设置，上限 128 MiB / 20 万条 |
| 更多 | 站源管理、用户管理、设置与备份、界面模式、关于 |

| 站源 | 浏览与播放 | 搜索 |
| --- | --- | --- |
| 红果 | 真人剧、漫剧、AI 剧及分集 | 联网搜索与官网搜索联想 |
| 韩小圈 | MacCMS 模板：最新韩剧 / 韩国电影 / 韩国综艺 / 韩国动漫，多线路分集 | 站源在线搜索 |
| 鬼片 | MacCMS 站点：鬼片 / 电视剧 / 动漫，多线路分集 | RSS 最新条目标题匹配（站点搜索已停用） |
| 青空 | 番剧、剧场动画、特摄及分集 | 站源在线搜索 |
| 黄豆 | 列表、VIP 标记及分集 | 筛选已加载短剧 |
| 剧果 | 热门、最新、接口分类、详情及可播放分集；签名 Cookie 接入在线播放、预加载和下载 | 站源在线搜索，支持继续加载 |
| 野果 | 接口实际分类、目录、详情及真实分集；按分集重新取流，保留 H.264 / H.265 地址 | 站源在线分页搜索 |
| 帝果 | 网页分类、目录、详情及分集；vplayer 签名解析 | 站源在线分页搜索 |
| 黄果视频 / 黄果 AI / 黄果旧版 | 列表、分类、详情及分集 | 筛选已加载短剧 |

站源可用性、清晰度和区域限制取决于源站及网络；应用不解除源站 VIP 或其他授权限制。

### 网络与资源设置

管理员从「设置与备份 → 网络与资源」选择自动、直连或手动代理，手动地址支持 HTTP、HTTPS、SOCKS5 / SOCKS5h。代理地址默认遮蔽、不进入备份；设备内播放服务始终绕过代理。自动模式读取系统静态代理与排除列表，恢复前台及每 30 秒更新；默认保留 TLS 校验，管理员显式开启允许本机代理替换证书时例外。

目录请求并发可设 1–6、间隔 0–5000 毫秒（默认 3 / 250ms）；下载并发默认 2、可设 1–6。仍保留前台优先、后台让行、取消、退避及请求数量边界。

资料补齐使用连续工作队列（最多 6 个工作者）、每批最多 64 部的目录索引及批量落盘，完成的结果随到随合并并批量落盘，取消或遇到验证等异常时保留已返回的内容。工作者仍受上述全局目录请求并发和间隔约束，后台预留前台请求容量。可点击「加快资料补齐」选择 6 / 100ms，再保存；不自动覆盖原设置。真实速度还取决于源站响应时间、每部请求数及限流。

### 局域网互联：追剧同步与推送播放

两台设备在同一局域网直连，自动同步追剧记录或接续播放，不依赖账号服务器。服务类型 `_zgj-link._tcp`（DNS-SD / mDNS），按设备 ID 合并多网卡地址，首次配对记录证书指纹，后续连接固定校验。

入口：「追剧 → 标题栏同步」或「设置与备份 → 设备互联」。手动同步默认「双向合并」，可选覆盖对方 / 覆盖本机，先「查看预览」再执行；自动同步仅发送改动条目与删除标记，播放期间约每 10 秒发送进度。冲突记录保留候选，用户从同步页逐项处理。

## 安装包与平台状态

| 平台 | 包与状态 |
| --- | --- |
| Android 8.0+ | 三架构（arm64-v8a / armeabi-v7a / x86_64）APK。**0.2.68+75 起改用新的签名密钥**，从更早版本升级需先卸载重装 |
| Windows 10/11 x64 | 完整 ZIP 解压后运行 `hongguojian.exe` / `zhenguojian.exe`，保留所有 DLL 与 `data`；局域网原生发现依赖 Windows 10 1903+ |
| Android TV | 与手机共用源码，自动识别电视模式并保持横屏；待电视 / 盒子实机验收 |
| iOS 15.1+ | 已加入工程、Go 核心链接、媒体依赖、文件管理与构建脚本；iOS 播放页禁用 media_kit_video 硬件纹理加速以规避 libmpv 渲染崩溃；待 Xcode 构建与真机验收，无已签名 IPA |

`INSTALL_FAILED_NO_MATCHING_ABIS` 表示 APK 与设备架构不匹配，请更换对应架构安装包。

### GitHub Actions

推送 `main` / `master`、`v*` 标签、提交 PR，或手动运行 **Build app packages**，会先检查再构建两版（默认与 `--all-sources`）：

| 产物 | 内容 |
| --- | --- |
| `*-android` | 三种架构 APK 和 SHA256 |
| `*-windows` | 完整 ZIP 和 SHA256 |
| `*-ios-unsigned` | 未签名 `.app` ZIP 和 SHA256，不能直接当已签名 IPA 安装 |

推送 `main` 且 android / ios / windows 全部构建成功时，自动创建 / 更新 GitHub Release（tag `app-v{version}`）。发布新版本前需先在 `pubspec.yaml` 提升 `version`，否则会覆盖同名 tag 的 Release。

Android 正式发布使用同一签名并递增构建号，在仓库 Secrets 配置：`ANDROID_KEYSTORE_BASE64`、`ANDROID_KEYSTORE_PASSWORD`、`ANDROID_KEY_ALIAS`、`ANDROID_KEY_PASSWORD`。未配置时生成 debug 签名预览包。

签名文件只放在本机，不进仓库。本地 `android/key.properties`：

~~~properties
storeFile=duanju_release.keystore
storePassword=你的密码
keyAlias=duanju
keyPassword=你的密码
~~~

密钥库放在 `android/app/duanju_release.keystore`。`android/.gitignore` 已忽略 `key.properties` 与 `**/*.keystore`，换机器时自行把密钥库和密码带过去。

## 开发与构建

Flutter `3.47.4`、Dart `3.12+`、Go `1.24.1+`、Python `3.10+`。Android 需要 JDK 17、SDK 36、NDK `28.2.13676358`；Windows 需要 Visual Studio C++ 桌面组件及 MinGW-w64 x64；iOS 需要 macOS、完整 Xcode 和 CocoaPods。

构建脚本对子进程默认设置 `GOPROXY=https://goproxy.cn,direct`、`GOSUMDB=off`，同名环境变量可覆盖。

~~~sh
python3 scripts/build_android.py                 # 红果鉴
python3 scripts/build_android.py --all-sources   # 真果鉴
python3 scripts/build_android.py --abi arm64-v8a
python3 scripts/build_android.py --cn-mirrors    # 国内镜像
~~~

~~~powershell
.\scripts\build_windows.ps1
.\scripts\build_windows.ps1 -AllSources
.\scripts\build_windows.ps1 -ChinaMirrors
~~~

~~~sh
python3 scripts/build_ios.py
python3 scripts/build_ios.py --all-sources
python3 scripts/build_ios.py --core-only [--simulator]
python3 scripts/build_ios.py --export-options /path/to/ExportOptions.plist
~~~

产物在 `dist/android`、`dist/windows`、`dist/ios`，红果版以 `hongguojian-` 开头，全站源版以 `zhenguojian-` 开头。

首次 Android 调试先编译对应架构核心，再运行：

~~~sh
python3 scripts/build_native.py --platform android --abi arm64-v8a
flutter pub get --enforce-lockfile
flutter run
~~~

调试全站源版：先给 `build_native.py` 加 `--all-sources`，再 `flutter run --dart-define=ALL_SOURCES=true`；iOS 对应 `build_ios.py --core-only --all-sources`。脚本会同步设置 Dart 常量和 Go 编译参数，应用启动时校验二者一致，避免混装原生库。

播放器使用 [media_kit](https://github.com/media-kit/media-kit) / libmpv，合并和导出使用 [FFmpegKit min-gpl](https://github.com/sk3llo/ffmpeg_kit_flutter)（含 GPL 媒体组件）。FFmpegKit 不参与正常播放或下载的转码。

### 测试

~~~sh
python3 -m unittest discover -s scripts -p 'test_*.py'
dart format --output=none --set-exit-if-changed lib test integration_test test_driver
dart analyze --fatal-infos lib test integration_test test_driver
flutter test --dart-define=DISABLE_REMOTE_IMAGES=true
flutter test --dart-define=DISABLE_REMOTE_IMAGES=true --dart-define=ALL_SOURCES=true
cd native
go test -race ./...
go test -race -ldflags="-X duanjuapp/native/core.buildAllSources=true" ./...
~~~

Flutter 测试建议加 `--concurrency=1` 并屏蔽远程图片；宿主媒体（本地合并 / 转码）用例需要本机同时具备 `ffmpeg` 与 `ffprobe`，缺失时会被跳过。

Android 设备回归（连接并授权 USB 调试、保持解锁）：

~~~sh
python3 scripts/create_test_media.py
python3 scripts/serve_test_media.py
adb reverse tcp:38473 tcp:38473
flutter drive --driver=test_driver/playback.dart --target=integration_test/playback_test.dart \
  --dart-define=DISABLE_REMOTE_IMAGES=true --dart-define=FIXTURE_BASE_URL=http://127.0.0.1:38473
~~~

结果在 `build/device-test/results/`；结束后停服务并 `adb reverse --remove tcp:38473`。

### 源码同步

~~~sh
python3 scripts/finish_task.py --message "本次实际完成的变更"
python3 scripts/sync_source.py --check
~~~

脚本只同步纯源码到同级 `../guoapp`，并生成 `真果·鉴-YYYYMMDDHHMM.zip` 源码压缩包；不执行 Git 提交、分支或推送。`finish_task.py` 在打包前会校验 `pubspec.yaml`、`lib/app_layout.dart`、`lib/main.dart`、`native/core/app_build.go` 四处的版本号一致，不一致直接失败。

## 目录结构

| 目录 | 内容 |
| --- | --- |
| `lib` | 页面、播放器、本地用户、FFI、下载和媒体处理 |
| `native/core`、`native/bridge` | 独立站源核心、缓存、下载、目录迁移及 C ABI |
| `android`、`windows`、`ios` | 平台工程与必要资源 |
| `assets/video_enhancement`、`packages/media_kit_libs_windows_video` | 增强 Shader 与许可、固定 Windows 媒体依赖插件 |
| `scripts`、`.github/workflows` | 构建、签名、验证、同步和版本快照 |
| `test`、`integration_test` | 自动化与设备回归 |

## 站源开发约定

站源是 Go 原生 provider（`native/core/provider_*.go`），不是运行期加载的 Python 源。新增站源需接入：`provider_huangguo.go`（常量 / 白名单 / `canonicalProviderSource` / `GetHuangguoChapters`）、`provider_media.go`（baseURL / host 反查 / 播放分派）、`app_categories.go`、`app_cover_metadata.go`、`app_runtime.go`（Config 字段 + 目录 / 搜索 / 详情分派 + 空页放行），并在 `lib/models.dart` 登记 `SourceSite`。

注意：Go RE2 正则**不支持 lookahead**。解析多线路播放列表时不能用 `(?=...)` 做分段，否则非捕获组会消耗下一段开头；应改用显式字符串截断（从容器标记之后查找下一段标记）。
