import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:shared_preferences/shared_preferences.dart';

import 'app_build.dart';
import 'local_store.dart';

class WebDavConfig {
  const WebDavConfig({
    this.uri = '',
    this.username = '',
    this.password = '',
    this.directory = defaultDirectory,
  });

  final String uri;
  final String username;
  final String password;
  final String directory;

  static const defaultDirectory = 'duanju';
  static const keyUri = 'webdavUri';
  static const keyUsername = 'webdavUsername';
  static const keyPassword = 'webdavPassword';
  static const keyDirectory = 'webdavDirectory';

  static Future<WebDavConfig> load(SharedPreferences preferences) async =>
      WebDavConfig(
        uri: preferences.getString(keyUri) ?? '',
        username: preferences.getString(keyUsername) ?? '',
        password: preferences.getString(keyPassword) ?? '',
        directory: preferences.getString(keyDirectory) ?? defaultDirectory,
      );

  Future<void> save(SharedPreferences preferences) async {
    await preferences.setString(keyUri, uri.trim());
    await preferences.setString(keyUsername, username.trim());
    await preferences.setString(keyPassword, password);
    await preferences.setString(keyDirectory, directory.trim());
  }

  String get effectiveDirectory =>
      directory.trim().isEmpty ? defaultDirectory : directory.trim();

  bool get isConfigured => uri.trim().isNotEmpty;

  WebDavConfig copyWith({
    String? uri,
    String? username,
    String? password,
    String? directory,
  }) => WebDavConfig(
    uri: uri ?? this.uri,
    username: username ?? this.username,
    password: password ?? this.password,
    directory: directory ?? this.directory,
  );

  Map<String, String> toJson() => {
    keyUri: Uri.tryParse(uri)?.replace(userInfo: '').toString() ?? '',
    keyUsername: username,
    keyDirectory: directory,
  };
}

class WebDavException implements Exception {
  const WebDavException(this.message);
  final String message;
  @override
  String toString() => message;
}

class WebDavClient {
  WebDavClient(this.config);

  final WebDavConfig config;

  static const _connectTimeout = Duration(seconds: 15);
  static const _ioTimeout = Duration(seconds: 90);
  static const maxBackupBytes = 8 * 1024 * 1024;

  Uri get _baseUri {
    var text = config.uri.trim();
    if (text.isEmpty) {
      throw const WebDavException('请先填写 WebDAV 地址');
    }
    if (!text.startsWith('http://') && !text.startsWith('https://')) {
      text = 'https://$text';
    }
    final parsed = Uri.parse(text).removeFragment();
    if (!parsed.hasAuthority || parsed.host.isEmpty) {
      throw const WebDavException('WebDAV 地址无效');
    }
    return parsed;
  }

  String get _username {
    if (config.username.isNotEmpty) return config.username;
    return _baseUri.userInfo.split(':').first;
  }

  String get _password {
    if (config.password.isNotEmpty) return config.password;
    final parts = _baseUri.userInfo.split(':');
    return parts.length > 1 ? parts.sublist(1).join(':') : '';
  }

  Uri _resolve(List<String> segments) {
    final base = _baseUri;
    final path = <String>[
      ...base.pathSegments.where((segment) => segment.isNotEmpty),
      ...segments.where((segment) => segment.isNotEmpty),
    ];
    if (path.any((segment) => segment == '.' || segment == '..')) {
      throw const WebDavException('备份路径不能包含相对目录');
    }
    return base.replace(pathSegments: path, userInfo: '');
  }

  Uri get directoryUri => _resolve(_directorySegments);
  Uri get backupFileUri => _resolve([..._directorySegments, backupFileName]);

  List<String> get _directorySegments {
    final raw = config.effectiveDirectory;
    return raw
        .split('/')
        .where((segment) => segment.trim().isNotEmpty)
        .toList();
  }

  String get backupFileName =>
      '$appSlug-backup-${Platform.operatingSystem}.json';

  Map<String, String> get _headers {
    final headers = <String, String>{HttpHeaders.acceptCharsetHeader: 'utf-8'};
    final user = _username;
    if (user.isNotEmpty || _password.isNotEmpty) {
      final credentials = base64.encode(utf8.encode('$user:$_password'));
      headers[HttpHeaders.authorizationHeader] = 'Basic $credentials';
    }
    return headers;
  }

  Future<HttpClientResponse> _send(
    String method,
    Uri url, {
    List<int>? body,
    Map<String, String>? headers,
  }) async {
    final client = HttpClient()
      ..connectionTimeout = _connectTimeout
      ..idleTimeout = _connectTimeout;
    try {
      final response = await _open(
        client,
        method,
        url,
        body: body,
        headers: headers,
      );
      await response.drain<void>().timeout(_ioTimeout);
      return response;
    } finally {
      client.close(force: true);
    }
  }

  Future<HttpClientResponse> _open(
    HttpClient client,
    String method,
    Uri url, {
    List<int>? body,
    Map<String, String>? headers,
  }) async {
    var target = url;
    for (var hop = 0; hop < 6; hop++) {
      final request = await client
          .openUrl(method, target)
          .timeout(_connectTimeout);
      request.followRedirects = false;
      request.headers.contentLength = body?.length ?? 0;
      _headers.forEach(request.headers.set);
      headers?.forEach(request.headers.set);
      if (body != null) request.add(body);
      final response = await request.close().timeout(_ioTimeout);
      if (!const [301, 302, 303, 307, 308].contains(response.statusCode))
        return response;
      final location = response.headers.value(HttpHeaders.locationHeader);
      if (location == null) return response;
      final next = target.resolve(location);
      if (next.origin != url.origin || next.userInfo.isNotEmpty) {
        throw const WebDavException('拒绝向其他站点重定向 WebDAV 凭据');
      }
      if (response.statusCode == 303 && method != 'GET' && method != 'HEAD') {
        throw const WebDavException('服务器要求改变请求方法，请使用最终 WebDAV 地址');
      }
      await response.drain<void>().timeout(_ioTimeout);
      target = next;
    }
    throw const WebDavException('WebDAV 重定向次数过多');
  }

  static String _describe(Object error) {
    if (error is WebDavException) return error.message;
    if (error is TimeoutException) return '连接超时，请检查网络与地址';
    if (error is HandshakeException) return 'TLS 握手失败，请检查地址与证书';
    if (error is SocketException) {
      return '无法连接服务器（${error.osError?.message ?? error.message}）';
    }
    return error.toString();
  }

  Future<void> connect() async {
    try {
      final probe = await _send('HEAD', _resolve([]));
      if (probe.statusCode == 401) {
        throw const WebDavException('WebDAV 认证失败，请检查账号密码');
      }
      final segments = _directorySegments;
      var current = <String>[];
      for (final segment in segments) {
        current = [...current, segment];
        final response = await _send('MKCOL', _resolve(current));
        if (response.statusCode >= 400 && response.statusCode != 405) {
          throw WebDavException('创建目录失败（$segment，状态码 ${response.statusCode}）');
        }
      }
    } on WebDavException {
      rethrow;
    } catch (error) {
      throw WebDavException(_describe(error));
    }
  }

  Future<void> write(String name, List<int> bytes) async {
    try {
      final target = _resolve([..._directorySegments, name]);
      final response = await _send('PUT', target, body: bytes);
      if (response.statusCode >= 300) {
        throw WebDavException('上传失败（状态码 ${response.statusCode}）');
      }
    } on WebDavException {
      rethrow;
    } catch (error) {
      throw WebDavException(_describe(error));
    }
  }

  Future<List<int>> read(String name) async {
    try {
      final target = _resolve([..._directorySegments, name]);
      final client = HttpClient()..connectionTimeout = _connectTimeout;
      try {
        final response = await _open(client, 'GET', target);
        if (response.statusCode == 404) {
          throw const WebDavException('远端没有找到备份文件');
        }
        if (response.statusCode >= 300) {
          throw WebDavException('下载失败（状态码 ${response.statusCode}）');
        }
        if (response.contentLength > maxBackupBytes) {
          throw const WebDavException('远端备份文件过大');
        }
        return await response
            .fold<List<int>>(<int>[], (bytes, chunk) {
              if (bytes.length + chunk.length > maxBackupBytes) {
                throw const WebDavException('远端备份文件过大');
              }
              return bytes..addAll(chunk);
            })
            .timeout(_ioTimeout);
      } finally {
        client.close(force: true);
      }
    } on WebDavException {
      rethrow;
    } catch (error) {
      throw WebDavException(_describe(error));
    }
  }

  Future<DateTime?> backupModifiedAt() async {
    try {
      final response = await _send('HEAD', backupFileUri);
      if (response.statusCode == 404) return null;
      if (response.statusCode == 401) {
        throw const WebDavException('WebDAV 认证失败，请检查账号密码');
      }
      if (response.statusCode >= 300) return null;
      final raw = response.headers.value(HttpHeaders.lastModifiedHeader);
      if (raw == null) return null;
      return HttpDate.parse(raw);
    } on WebDavException {
      rethrow;
    } catch (_) {
      return null;
    }
  }
}

class WebDavSync {
  const WebDavSync._();

  static const lastBackupAtKey = 'webdavLastBackupAt';

  static Future<void> backup({
    required SharedPreferences preferences,
    required LocalStore store,
  }) async {
    final config = await WebDavConfig.load(preferences);
    if (!config.isConfigured) {
      throw const WebDavException('请先填写并保存 WebDAV 配置');
    }
    final content = await store.exportBackup();
    final client = WebDavClient(config);
    await client.connect();
    await client.write(client.backupFileName, utf8.encode(content));
    await preferences.setString(
      lastBackupAtKey,
      DateTime.now().toIso8601String(),
    );
  }

  static Future<String> fetchBackup(SharedPreferences preferences) async {
    final config = await WebDavConfig.load(preferences);
    if (!config.isConfigured) {
      throw const WebDavException('请先填写并保存 WebDAV 配置');
    }
    final client = WebDavClient(config);
    await client.connect();
    final bytes = await client.read(client.backupFileName);
    final content = utf8.decode(bytes);
    if (content.length > 8 * 1024 * 1024) {
      throw const WebDavException('远端备份文件过大');
    }
    return content;
  }

  static Future<String?> testConnection(WebDavConfig config) async {
    try {
      await WebDavClient(config).connect();
      return null;
    } on WebDavException catch (error) {
      return error.message;
    } catch (error) {
      return error.toString();
    }
  }

  static Future<DateTime?> lastBackupAt(SharedPreferences preferences) async {
    final raw = preferences.getString(lastBackupAtKey);
    return raw == null ? null : DateTime.tryParse(raw);
  }
}
