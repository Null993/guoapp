import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_inappwebview/flutter_inappwebview.dart';

Future<String> sourceLoginCookies(
  InAppWebViewController controller,
  Uri url,
) async {
  if (Platform.isWindows) {
    final result = await controller.callDevToolsProtocolMethod(
      methodName: 'Network.getCookies',
      parameters: {
        'urls': [url.toString()],
      },
    );
    final data = result is String ? jsonDecode(result) : result;
    return [
      for (final entry in (data as Map)['cookies'] as List)
        '${entry['name']}=${entry['value']}',
    ].join('; ');
  }
  final cookies = await CookieManager.instance().getCookies(
    url: WebUri(url.toString()),
    webViewController: controller,
  );
  return cookies.map((entry) => '${entry.name}=${entry.value}').join('; ');
}

Map<String, String> sourceLoginSession({
  required String source,
  required Uri site,
  required Uri origin,
  required Map<String, dynamic> page,
  required String cookie,
}) {
  final current = Uri.tryParse(page['url'] as String? ?? '');
  final trusted =
      current != null &&
      current.scheme == 'https' &&
      current.userInfo.isEmpty &&
      current.port == site.port &&
      (current.host == site.host ||
          source == 'sorani' &&
              const {'sorani.net', 'www.sorani.net'}.contains(current.host));
  if (!trusted) throw StateError('请返回当前站点完成登录后再提取会话');
  final token = source == 'sorani' ? page['accessToken'] as String? ?? '' : '';
  if (source == 'sorani' && token.trim().isEmpty) {
    throw StateError('尚未检测到青空登录令牌，请先完成登录');
  }
  if (token.isEmpty && cookie.isEmpty) {
    throw StateError('尚未取得站点会话，请完成登录或验证后重试');
  }
  return {
    'origin': origin.origin,
    'cookie': cookie,
    'userAgent': page['userAgent'] as String? ?? '',
    'accessToken': token,
    'deviceId': source == 'sorani' ? page['deviceId'] as String? ?? '' : '',
  };
}

class SourceLoginScreen extends StatefulWidget {
  const SourceLoginScreen({
    super.key,
    required this.source,
    required this.name,
    required this.url,
    required this.origin,
  });

  final String source;
  final String name;
  final Uri url;
  final Uri origin;

  @override
  State<SourceLoginScreen> createState() => _SourceLoginScreenState();
}

class _SourceLoginScreenState extends State<SourceLoginScreen> {
  InAppWebViewController? _controller;
  Timer? _timer;
  String _address = '';
  String _error = '';
  int _progress = 0;
  bool _extracting = false;
  bool _returned = false;

  @override
  void initState() {
    super.initState();
    PlatformInAppWebViewController.debugLoggingSettings = DebugLoggingSettings(
      enabled: false,
    );
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _extract({bool automatic = false}) async {
    final controller = _controller;
    if (controller == null ||
        _extracting ||
        _returned ||
        ModalRoute.of(context)?.isCurrent != true) {
      return;
    }
    _extracting = true;
    try {
      final raw = await controller.evaluateJavascript(
        source:
            '''
(() => {
  const hosts = ${jsonEncode(widget.source == 'sorani' ? ['sorani.net', 'www.sorani.net'] : [widget.url.host])};
  if (location.protocol !== 'https:' || !hosts.includes(location.hostname) || location.port !== ${jsonEncode(widget.url.port == 443 ? '' : '${widget.url.port}')}) return '{}';
  let token = {};
  try { token = JSON.parse(localStorage.getItem('sorani_auth_token') || sessionStorage.getItem('sorani_auth_token') || '{}'); } catch (_) {}
  return JSON.stringify({url: location.href, userAgent: navigator.userAgent,
    accessToken: typeof token.accessToken === 'string' ? token.accessToken : '',
    deviceId: localStorage.getItem('sorani_guest_key') || ''});
})()
''',
      );
      if (!mounted) return;
      final page = jsonDecode(raw as String) as Map<String, dynamic>;
      if (automatic && (page['accessToken'] as String? ?? '').isEmpty) return;
      final cookieUrl = widget.source == 'sorani'
          ? widget.origin.resolve('/sorani-cms/api/')
          : widget.origin;
      final cookie = await sourceLoginCookies(controller, cookieUrl);
      final session = sourceLoginSession(
        source: widget.source,
        site: widget.url,
        origin: widget.origin,
        page: page,
        cookie: cookie,
      );
      if (mounted && ModalRoute.of(context)?.isCurrent == true) {
        _returned = true;
        _timer?.cancel();
        Navigator.of(context).pop(session);
      }
    } catch (_) {
      if (mounted && !automatic) {
        setState(() => _error = '尚未取得有效的本站登录会话，请确认登录成功后重试');
      }
    } finally {
      _extracting = false;
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: Text('${widget.name}站点登录'),
      actions: [
        IconButton(
          tooltip: '重新加载',
          onPressed: () => _controller?.reload(),
          icon: const Icon(Icons.refresh),
        ),
        TextButton(onPressed: () => _extract(), child: const Text('登录 / 验证完成')),
      ],
    ),
    body: Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(8),
          child: Text(_address.isEmpty ? widget.url.toString() : _address),
        ),
        const Padding(
          padding: EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          child: Text('在此登录本人站点账号。成功后提取本站会话，返回授权窗口保存。'),
        ),
        if (_error.isNotEmpty)
          Padding(
            padding: const EdgeInsets.all(8),
            child: Text(
              _error,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ),
        if (_progress < 100) LinearProgressIndicator(value: _progress / 100),
        Expanded(
          child: InAppWebView(
            initialUrlRequest: URLRequest(url: WebUri(widget.url.toString())),
            initialSettings: InAppWebViewSettings(
              javaScriptEnabled: true,
              domStorageEnabled: true,
              incognito: true,
              useShouldOverrideUrlLoading: true,
              javaScriptCanOpenWindowsAutomatically: false,
            ),
            onWebViewCreated: (controller) {
              _controller = controller;
              if (widget.source == 'sorani') {
                _timer = Timer.periodic(const Duration(seconds: 2), (_) {
                  unawaited(_extract(automatic: true));
                });
              }
            },
            shouldOverrideUrlLoading: (_, action) async =>
                action.request.url?.scheme == 'https'
                ? NavigationActionPolicy.ALLOW
                : NavigationActionPolicy.CANCEL,
            onProgressChanged: (_, progress) {
              if (mounted) setState(() => _progress = progress);
            },
            onLoadStart: (_, url) {
              if (mounted) {
                setState(() {
                  _address = url?.toString() ?? '';
                  _error = '';
                });
              }
            },
            onUpdateVisitedHistory: (_, url, _) {
              if (mounted) setState(() => _address = url?.toString() ?? '');
            },
            onReceivedError: (_, request, _) {
              if (mounted && request.isForMainFrame == true) {
                setState(() => _error = '站点连接失败，请检查网络后重新加载');
              }
            },
            onReceivedHttpError: (_, request, response) {
              if (mounted && request.isForMainFrame == true) {
                setState(
                  () => _error = '站点返回 HTTP ${response.statusCode}，请稍后重试',
                );
              }
            },
          ),
        ),
      ],
    ),
  );
}
