import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_inappwebview/flutter_inappwebview.dart' hide Cookie;

import 'source_login_screen.dart';

Future<Map<String, bool>> smokeSourceLoginWebView() async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final completed = Completer<Map<String, bool>>();
  server.listen((request) async {
    request.response.cookies.add(
      Cookie('session', 'synthetic')..httpOnly = true,
    );
    request.response.headers.contentType = ContentType.html;
    request.response.write(
      '<html><body>登录组件合成验证<script>localStorage.setItem("sorani_auth_token",JSON.stringify({accessToken:"synthetic-token"}));</script></body></html>',
    );
    await request.response.close();
  });
  final address = Uri.parse('http://127.0.0.1:${server.port}/');
  try {
    runApp(
      MaterialApp(
        home: Scaffold(
          body: InAppWebView(
            initialUrlRequest: URLRequest(url: WebUri(address.toString())),
            initialSettings: InAppWebViewSettings(
              javaScriptEnabled: true,
              domStorageEnabled: true,
              incognito: true,
            ),
            onLoadStop: (controller, _) async {
              if (completed.isCompleted) return;
              try {
                final token = await controller.evaluateJavascript(
                  source:
                      "JSON.parse(localStorage.getItem('sorani_auth_token')).accessToken",
                );
                final cookie = await sourceLoginCookies(controller, address);
                final result = {
                  'loginWebView': token == 'synthetic-token',
                  'loginHttpOnlyCookie': cookie.contains('session=synthetic'),
                };
                if (!result.values.every((value) => value)) {
                  throw StateError('登录组件会话提取失败');
                }
                if (!completed.isCompleted) completed.complete(result);
              } catch (error, stack) {
                if (!completed.isCompleted) {
                  completed.completeError(error, stack);
                }
              }
            },
          ),
        ),
      ),
    );
    return await completed.future.timeout(const Duration(seconds: 30));
  } finally {
    runApp(const SizedBox.shrink());
    await WidgetsBinding.instance.endOfFrame;
    await server.close(force: true);
  }
}
