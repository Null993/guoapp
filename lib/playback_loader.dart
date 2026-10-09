import 'core_bridge.dart';
import 'diary_service.dart';
import 'models.dart';

class PlaybackLoader {
  PlaybackLoader(this.repository);

  final AppRepository repository;
  int _generation = 0;
  bool _closed = false;

  Future<PlaybackPlan?> load(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool localOnly = false,
    bool online = false,
  }) => _load(() async {
    if (online) {
      return repository.resolveOnline(drama, episode, quality: quality);
    }
    if (localOnly) {
      final plan = await repository.localPlayback(drama, episode);
      if (plan == null || !plan.local) {
        throw AppFailure('本地视频不可用，请重新下载或选择在线播放。', code: 'local_media');
      }
      return plan;
    }
    return repository.resolve(drama, episode, quality: quality);
  });

  Future<PlaybackPlan?> fallback(PlaybackPlan current) =>
      _load(() => repository.fallback(current));

  Future<PlaybackPlan?> use(PlaybackPlan prepared) =>
      _load(() async => prepared);

  Future<PlaybackPlan?> _load(Future<PlaybackPlan> Function() resolve) async {
    if (_closed) {
      return null;
    }
    final generation = ++_generation;
    try {
      await repository.cancelPlayback();
      if (_closed || generation != _generation) {
        return null;
      }
      final plan = await resolve();
      if (_closed || generation != _generation) {
        await repository.release(plan.session);
        return null;
      }
      return plan;
    } catch (error) {
      if (_closed || generation != _generation) {
        return null;
      }
      DiaryService.add('[Play] 获取播放地址失败: ${error.runtimeType}: $error');
      rethrow;
    }
  }

  Future<void> close() async {
    _closed = true;
    _generation++;
    await repository.cancelPlayback();
  }
}
