import 'package:duanju_app/source_login_screen.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  Map<String, String> capture(
    String address, {
    String token = 'synthetic-token',
  }) => sourceLoginSession(
    source: 'sorani',
    site: Uri.parse('https://www.sorani.net'),
    origin: Uri.parse('https://api.sorani.cc'),
    page: {
      'url': address,
      'accessToken': token,
      'deviceId': 'synthetic-guest',
      'userAgent': 'synthetic-agent',
    },
    cookie: 'session=synthetic',
  );

  test('login extraction binds website token to its API origin', () {
    final session = capture('https://sorani.net/account');
    expect(session['origin'], 'https://api.sorani.cc');
    expect(session['accessToken'], 'synthetic-token');
    expect(session['cookie'], 'session=synthetic');
  });

  test('login extraction rejects foreign and downgraded pages', () {
    for (final address in [
      'https://sorani.net.example.test',
      'https://example.test',
      'http://www.sorani.net',
      'https://www.sorani.net:8443',
      'https://user@www.sorani.net',
    ]) {
      expect(() => capture(address), throwsStateError);
    }
  });

  test('Sorani tracking cookies do not count as a logged-in session', () {
    expect(
      () => capture('https://www.sorani.net', token: ''),
      throwsStateError,
    );
  });

  test('generic source extraction uses only its own cookies', () {
    final session = sourceLoginSession(
      source: 'huangguo-video',
      site: Uri.parse('https://huangguo.video'),
      origin: Uri.parse('https://huangguo.video'),
      page: {
        'url': 'https://huangguo.video/account',
        'accessToken': 'unrelated',
      },
      cookie: 'session=synthetic',
    );
    expect(session['accessToken'], isEmpty);
    expect(session['cookie'], 'session=synthetic');
  });
}
