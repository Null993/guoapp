import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/webdav_settings_screen.dart';
import 'package:duanju_app/webdav_sync.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class _RealHttpOverrides extends HttpOverrides {}

class _DavServer {
  final String username = 'user';
  final String password = 'pass';
  final Map<String, List<int>> files = {};
  final Set<String> collections = {};
  final List<String> requests = [];
  final Map<String, String> redirects = {};
  HttpServer? _server;

  Future<void> start() async {
    _server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    _server!.listen(_handle);
  }

  Future<void> stop() async {
    await _server?.close(force: true);
    _server = null;
  }

  int get port => _server!.port;
  Uri get baseUri => Uri.parse('http://127.0.0.1:$port/dav');

  bool _authorized(HttpRequest request) {
    final header = request.headers.value(HttpHeaders.authorizationHeader);
    if (header == null || !header.startsWith('Basic ')) return false;
    try {
      final decoded = utf8.decode(base64.decode(header.substring(6)));
      return decoded == '$username:$password';
    } catch (_) {
      return false;
    }
  }

  Future<void> _handle(HttpRequest request) async {
    requests.add('${request.method} ${request.uri.path}');
    if (!_authorized(request)) {
      request.response.statusCode = 401;
      request.response.headers.set(
        HttpHeaders.wwwAuthenticateHeader,
        'Basic realm="test"',
      );
      await request.response.close();
      return;
    }
    final path = request.uri.path;
    if (redirects.containsKey(path)) {
      request.response.statusCode = 307;
      request.response.headers.set(
        HttpHeaders.locationHeader,
        redirects[path]!,
      );
      await request.response.close();
      return;
    }
    switch (request.method) {
      case 'MKCOL':
        if (collections.contains(path)) {
          request.response.statusCode = 405;
        } else {
          collections.add(path);
          request.response.statusCode = 201;
        }
      case 'PUT':
        final body = await request.fold<List<int>>(
          <int>[],
          (bytes, chunk) => bytes..addAll(chunk),
        );
        files[path] = body;
        request.response.statusCode = 201;
      case 'GET':
        final bytes = files[path];
        if (bytes == null) {
          request.response.statusCode = 404;
        } else {
          request.response.statusCode = 200;
          request.response.headers.set(
            HttpHeaders.lastModifiedHeader,
            HttpDate.format(DateTime.utc(2026)),
          );
          request.response.add(bytes);
        }
      case 'DELETE':
        files.remove(path);
        collections.remove(path);
        request.response.statusCode = 204;
      case 'HEAD':
        if (files.containsKey(path)) {
          request.response.statusCode = 200;
          request.response.headers.set(
            HttpHeaders.lastModifiedHeader,
            HttpDate.format(DateTime.utc(2026)),
          );
        } else {
          request.response.statusCode = 404;
        }
      default:
        request.response.statusCode = 405;
    }
    await request.response.close();
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  HttpOverrides.global = _RealHttpOverrides();

  group('WebDavConfig', () {
    test('save and load roundtrip through preferences', () async {
      SharedPreferences.setMockInitialValues({});
      final preferences = await SharedPreferences.getInstance();
      const config = WebDavConfig(
        uri: 'https://dav.example.com/dav',
        username: 'user',
        password: 'pass',
        directory: 'duanju-backup',
      );
      await config.save(preferences);
      final loaded = await WebDavConfig.load(preferences);
      expect(loaded.uri, config.uri);
      expect(loaded.username, config.username);
      expect(loaded.password, config.password);
      expect(loaded.directory, config.directory);
      expect(loaded.isConfigured, isTrue);
    });

    test('missing preferences fall back to defaults', () async {
      SharedPreferences.setMockInitialValues({});
      final preferences = await SharedPreferences.getInstance();
      final config = await WebDavConfig.load(preferences);
      expect(config.uri, isEmpty);
      expect(config.username, isEmpty);
      expect(config.directory, WebDavConfig.defaultDirectory);
      expect(config.effectiveDirectory, WebDavConfig.defaultDirectory);
      expect(config.isConfigured, isFalse);
    });

    test('blank directory falls back to the default directory', () {
      const config = WebDavConfig(
        uri: 'https://dav.example.com',
        directory: '  ',
      );
      expect(config.effectiveDirectory, WebDavConfig.defaultDirectory);
    });

    test('scheme defaults to https when the address omits it', () async {
      final server = _DavServer();
      await server.start();
      addTearDown(server.stop);
      final client = WebDavClient(
        WebDavConfig(
          uri: '127.0.0.1:${server.port}/dav',
          username: server.username,
          password: server.password,
        ),
      );
      final target = client.backupFileUri;
      expect(target.scheme, 'https');
      expect(target.host, '127.0.0.1');
      expect(target.port, server.port);
      expect(
        target.path,
        '/dav/duanju/$appSlug-backup-${Platform.operatingSystem}.json',
      );
    });
  });

  group('WebDavClient', () {
    late _DavServer server;
    late WebDavClient client;

    setUp(() async {
      server = _DavServer();
      await server.start();
      client = WebDavClient(
        WebDavConfig(
          uri: server.baseUri.toString(),
          username: server.username,
          password: server.password,
          directory: 'duanju/nested',
        ),
      );
    });

    tearDown(() async {
      await server.stop();
    });

    test('connect creates every directory level', () async {
      await client.connect();
      expect(server.collections, contains('/dav/duanju'));
      expect(server.collections, contains('/dav/duanju/nested'));
      expect(server.requests, contains('HEAD /dav'));
      expect(server.requests, contains('MKCOL /dav/duanju'));
    });

    test('write then read roundtrips the payload', () async {
      await client.connect();
      final payload = utf8.encode('{"schema":1,"hello":"webdav"}');
      await client.write(client.backupFileName, payload);
      expect(
        server.files.containsKey('/dav/duanju/nested/${client.backupFileName}'),
        isTrue,
      );
      final read = await client.read(client.backupFileName);
      expect(utf8.decode(read), '{"schema":1,"hello":"webdav"}');
    });

    test(
      'write overwrites without deleting the previous backup first',
      () async {
        await client.connect();
        final target = '/dav/duanju/nested/${client.backupFileName}';
        await client.write(client.backupFileName, utf8.encode('first'));
        await client.write(client.backupFileName, utf8.encode('second'));
        expect(server.requests.where((r) => r == 'DELETE $target'), isEmpty);
        expect(utf8.decode(await client.read(client.backupFileName)), 'second');
      },
    );

    test('oversized remote backups are rejected during download', () async {
      await client.connect();
      final target = '/dav/duanju/nested/${client.backupFileName}';
      server.files[target] = List.filled(WebDavClient.maxBackupBytes + 1, 65);
      await expectLater(
        client.read(client.backupFileName),
        throwsA(isA<WebDavException>()),
      );
    });

    test('follows only same-origin redirects', () async {
      await client.connect();
      final target = '/dav/duanju/nested/${client.backupFileName}';
      server.redirects[target] = '/dav/redirected.json';
      server.files['/dav/redirected.json'] = utf8.encode('same origin');
      expect(
        utf8.decode(await client.read(client.backupFileName)),
        'same origin',
      );
      server.redirects[target] = 'https://other.example.test/backup.json';
      await expectLater(
        client.read(client.backupFileName),
        throwsA(isA<WebDavException>()),
      );
    });

    test('backup file name is scoped to app and platform', () {
      expect(
        client.backupFileName,
        '$appSlug-backup-${Platform.operatingSystem}.json',
      );
    });

    test('backupModifiedAt parses the server timestamp', () async {
      await client.connect();
      expect(await client.backupModifiedAt(), isNull);
      await client.write(client.backupFileName, utf8.encode('x'));
      final modified = await client.backupModifiedAt();
      expect(modified, DateTime.utc(2026));
    });

    test('reading a missing backup reports a readable error', () async {
      await client.connect();
      await expectLater(
        client.read(client.backupFileName),
        throwsA(
          isA<WebDavException>().having(
            (error) => error.message,
            'message',
            contains('没有找到备份文件'),
          ),
        ),
      );
    });

    test('wrong password surfaces an authentication error', () async {
      final bad = WebDavClient(
        WebDavConfig(
          uri: server.baseUri.toString(),
          username: server.username,
          password: 'wrong',
          directory: 'duanju',
        ),
      );
      await expectLater(
        bad.connect(),
        throwsA(
          isA<WebDavException>().having(
            (error) => error.message,
            'message',
            contains('认证失败'),
          ),
        ),
      );
    });

    test('unreachable server reports a connection error', () async {
      final dead = WebDavClient(
        const WebDavConfig(
          uri: 'http://127.0.0.1:1/dav',
          username: 'user',
          password: 'pass',
          directory: 'duanju',
        ),
      );
      await expectLater(
        dead.connect(),
        throwsA(
          isA<WebDavException>().having(
            (error) => error.message,
            'message',
            contains('无法连接服务器'),
          ),
        ),
      );
    });
  });

  group('WebDavSync', () {
    late _DavServer server;

    setUp(() async {
      server = _DavServer();
      await server.start();
    });

    tearDown(() async {
      await server.stop();
    });

    Future<(SharedPreferences, LocalStore)> createStore() async {
      SharedPreferences.setMockInitialValues({});
      final preferences = await SharedPreferences.getInstance();
      return (preferences, testStore(preferences));
    }

    test('backup uploads the local export payload', () async {
      final (preferences, store) = await createStore();
      addTearDown(store.dispose);
      await WebDavConfig(
        uri: server.baseUri.toString(),
        username: server.username,
        password: server.password,
      ).save(preferences);

      await WebDavSync.backup(preferences: preferences, store: store);

      final name = '$appSlug-backup-${Platform.operatingSystem}.json';
      final uploaded = server.files['/dav/duanju/$name'];
      expect(uploaded, isNotNull);
      final restarted = testStore(preferences);
      addTearDown(restarted.dispose);
      final data = restarted.validateBackup(utf8.decode(uploaded!));
      expect(data['schema'], 1);
      expect(data['app'], 'zhenguojian');
      expect(await WebDavSync.lastBackupAt(preferences), isNotNull);
    });

    test(
      'fetchBackup returns the uploaded payload and reports missing files',
      () async {
        final (preferences, store) = await createStore();
        addTearDown(store.dispose);
        await WebDavConfig(
          uri: server.baseUri.toString(),
          username: server.username,
          password: server.password,
        ).save(preferences);

        await WebDavSync.backup(preferences: preferences, store: store);
        final content = await WebDavSync.fetchBackup(preferences);
        final restarted = testStore(preferences);
        addTearDown(restarted.dispose);
        expect(restarted.validateBackup(content)['app'], 'zhenguojian');

        server.files.clear();
        await expectLater(
          WebDavSync.fetchBackup(preferences),
          throwsA(
            isA<WebDavException>().having(
              (error) => error.message,
              'message',
              contains('没有找到备份文件'),
            ),
          ),
        );
      },
    );

    test('backup rejects an unconfigured server before any request', () async {
      final (preferences, store) = await createStore();
      addTearDown(store.dispose);
      await expectLater(
        WebDavSync.backup(preferences: preferences, store: store),
        throwsA(
          isA<WebDavException>().having(
            (error) => error.message,
            'message',
            contains('请先填写'),
          ),
        ),
      );
      expect(server.requests, isEmpty);
    });

    test('testConnection reports success and failure', () async {
      final good = WebDavConfig(
        uri: server.baseUri.toString(),
        username: server.username,
        password: server.password,
      );
      expect(await WebDavSync.testConnection(good), isNull);
      final bad = good.copyWith(password: 'wrong');
      expect(await WebDavSync.testConnection(bad), contains('认证失败'));
      final unconfigured = good.copyWith(uri: '');
      expect(await WebDavSync.testConnection(unconfigured), contains('请先填写'));
    });
  });

  testWidgets('settings screen saves config, tests and uploads', (
    tester,
  ) async {
    final server = _DavServer();
    await server.start();
    addTearDown(server.stop);
    SharedPreferences.setMockInitialValues({});
    final preferences = await SharedPreferences.getInstance();
    final store = testStore(preferences);
    addTearDown(store.dispose);

    Future<void> pumpUntilText(String text) async {
      for (var i = 0; i < 80; i++) {
        await tester.runAsync(
          () => Future<void>.delayed(const Duration(milliseconds: 34)),
        );
        await tester.pump(const Duration(milliseconds: 16));
        if (find.text(text).evaluate().isNotEmpty) return;
      }
    }

    Future<void> waitUntilIdle() async {
      for (var i = 0; i < 120; i++) {
        await tester.runAsync(
          () => Future<void>.delayed(const Duration(milliseconds: 34)),
        );
        await tester.pump(const Duration(milliseconds: 16));
        final buttons = tester.widgetList<FilledButton>(
          find.byType(FilledButton),
        );
        if (buttons.isNotEmpty &&
            buttons.every((button) => button.onPressed != null)) {
          return;
        }
      }
    }

    await tester.pumpWidget(
      MaterialApp(home: WebDavSettingsScreen(store: store)),
    );
    await tester.pumpAndSettle();

    expect(find.text('WebDAV 备份'), findsOneWidget);
    expect(find.byType(TextField), findsNWidgets(4));
    expect(find.text('保存并测试连接'), findsOneWidget);
    expect(find.text('备份到 WebDAV'), findsOneWidget);
    expect(find.text('从 WebDAV 恢复'), findsOneWidget);

    await tester.enterText(
      find.byType(TextField).at(0),
      'http://127.0.0.1:${server.port}/dav',
    );
    await tester.enterText(find.byType(TextField).at(1), server.username);
    await tester.enterText(find.byType(TextField).at(2), server.password);

    await tester.tap(find.text('保存并测试连接'));
    await tester.pump();
    await pumpUntilText('配置已保存，连接正常');
    expect(find.text('配置已保存，连接正常'), findsOneWidget);
    expect(server.collections, contains('/dav/duanju'));

    await waitUntilIdle();
    tester
        .state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger))
        .hideCurrentSnackBar();
    await tester.pump();

    await tester.tap(find.text('备份到 WebDAV'));
    await tester.pump();
    await pumpUntilText('备份已上传到 WebDAV');
    expect(find.text('备份已上传到 WebDAV'), findsOneWidget);
    final name = '$appSlug-backup-${Platform.operatingSystem}.json';
    expect(server.files.containsKey('/dav/duanju/$name'), isTrue);
    expect(await WebDavSync.lastBackupAt(preferences), isNotNull);

    await waitUntilIdle();
    final cardTexts = tester
        .widgetList<Text>(
          find.descendant(of: find.byType(Card), matching: find.byType(Text)),
        )
        .map((text) => text.data ?? '')
        .join(' ');
    expect(cardTexts, contains('2026-'));
    tester
        .state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger))
        .hideCurrentSnackBar();
    await tester.pump();
    await tester.runAsync(server.stop);
  });
}
