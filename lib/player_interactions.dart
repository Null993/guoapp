import 'dart:async';
import 'package:flutter/gestures.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:media_kit/media_kit.dart';

import 'app_layout.dart';
import 'widgets.dart';

enum SwipeAction { none, brightness, volume, fullscreen, horizontal }

enum SpeedLockHint {
  none,
  swipeUpToLock,
  releaseToLock,
  lockedConfirm,
  swipeDownToUnlock,
  releaseToUnlock,
  unlockedConfirm,
}

class GestureHudState {
  const GestureHudState({
    this.type = SwipeAction.none,
    this.value = 0.0,
    this.seekTarget = Duration.zero,
    this.seekDelta = 0,
    this.cancelSeek = false,
  });

  final SwipeAction type;
  final double value; // 0.0 ~ 1.0
  final Duration seekTarget;
  final int seekDelta;
  final bool cancelSeek;

  bool get isSeek => type == SwipeAction.horizontal;
  bool get isBrightness => type == SwipeAction.brightness;
  bool get isVolume => type == SwipeAction.volume;
}

class PlayerGestureOptions {
  const PlayerGestureOptions({
    this.slideVolumeBrightness = true,
    this.slideFullscreen = true,
    this.quickDoubleTap = false,
    this.doubleTapSeekSeconds = 10,
    this.dragSeekSeconds = 60,
    this.longPressSpeed = 3.0,
    this.autoLongPressSpeed = false,
  });

  final bool slideVolumeBrightness;
  final bool slideFullscreen;
  final bool quickDoubleTap;
  final int doubleTapSeekSeconds;

  final int dragSeekSeconds;
  final double longPressSpeed;
  final bool autoLongPressSpeed;

  PlayerGestureOptions copyWith({
    bool? slideVolumeBrightness,
    bool? slideFullscreen,
    bool? quickDoubleTap,
    int? doubleTapSeekSeconds,
    int? dragSeekSeconds,
    double? longPressSpeed,
    bool? autoLongPressSpeed,
  }) => PlayerGestureOptions(
    slideVolumeBrightness: slideVolumeBrightness ?? this.slideVolumeBrightness,
    slideFullscreen: slideFullscreen ?? this.slideFullscreen,
    quickDoubleTap: quickDoubleTap ?? this.quickDoubleTap,
    doubleTapSeekSeconds: doubleTapSeekSeconds ?? this.doubleTapSeekSeconds,
    dragSeekSeconds: dragSeekSeconds ?? this.dragSeekSeconds,
    longPressSpeed: longPressSpeed ?? this.longPressSpeed,
    autoLongPressSpeed: autoLongPressSpeed ?? this.autoLongPressSpeed,
  );
}

class PlayerInteractions extends ChangeNotifier {
  PlayerInteractions({
    required this.player,
    required this.available,
    required this.baseSpeed,
    required this.onTogglePlayback,
    required this.onFullscreen,
    required this.onSeek,
    this.options = const PlayerGestureOptions(),
    this.onEpisode,
    VoidCallback? onControlsToggle,
  }) : _onControlsToggle = onControlsToggle {
    _playing = player.stream.playing.listen((playing) {
      if (!playing) cancel();
    });
    AppDevice.getBrightness()
        .then((val) {
          if (!_disposed) {
            _brightness = val;
            notifyListeners();
          }
        })
        .catchError((_) {});
  }

  final Player player;
  final bool Function() available;
  final double Function() baseSpeed;
  final VoidCallback onTogglePlayback;

  final ValueChanged<bool> onFullscreen;
  final Future<void> Function(Duration) onSeek;
  final String Function(int direction)? onEpisode;
  final VoidCallback? _onControlsToggle;
  PlayerGestureOptions options;

  late final StreamSubscription<bool> _playing;
  Timer? _holdTimer;
  Timer? _hintTimer;
  Timer? _hudTimer;
  Timer? _lockHintTimer;
  Future<void> _rates = Future<void>.value();
  final Set<int> _pointers = {};
  int? _pointer;
  Offset? _origin;
  Offset? _lastPosition;
  bool _swipeEnabled = false;
  bool _moved = false;
  bool _held = false;
  bool _boosting = false;
  bool _keyboardHold = false;
  bool _cancelUntilRelease = false;
  bool _disposed = false;
  double _unmutedVolume = 100;
  String _feedback = '';
  DateTime _ignoreTapUntil = DateTime(2000);
  SwipeAction _swipeAction = SwipeAction.none;
  GestureHudState _hudState = const GestureHudState();
  double _brightness = 0.5;
  double _initialBrightness = 0.5;
  double _viewWidth = 0.0;
  double _viewHeight = 0.0;
  double _lastSeekDx = 0.0;
  bool _fullscreenTriggered = false;

  double _initialVolume = 100;

  int _brightnessGestureId = 0;

  bool _hasToasted = false;

  bool _speedLocked = false;
  bool _speedLockArmed = false;
  SpeedLockHint _speedLockHint = SpeedLockHint.none;
  Offset? _longPressOrigin;

  GestureHudState get hudState => _hudState;

  bool fullscreen = false;
  double get brightness => _brightness;
  bool get isBrightnessActive => _hudState.type == SwipeAction.brightness;
  String get feedback => _feedback;
  bool get boosting => _boosting;
  bool get speedLocked => _speedLocked;
  SpeedLockHint get speedLockHint => _speedLockHint;
  bool get suppressTap => DateTime.now().isBefore(_ignoreTapUntil);
  Future<void> get pendingRates => _rates;

  void updateOptions(PlayerGestureOptions value) {
    options = value;
  }

  void setBrightnessDirect(double value) {
    if (_disposed) return;
    _brightness = value.clamp(0.01, 1.0);
    AppDevice.setBrightness(_brightness);
    _showHud(SwipeAction.brightness, _brightness);
  }

  void dismissBrightnessHud() {
    _scheduleDismissHud();
  }

  void hint(String message, {bool persistent = false}) {
    if (_disposed) return;
    _hintTimer?.cancel();
    if (_feedback != message) {
      _feedback = message;
      notifyListeners();
    }
    if (!persistent && message.isNotEmpty) {
      _hintTimer = Timer(const Duration(milliseconds: 1200), () {
        hint(
          _boosting && !_speedLocked ? '${_currentBoostSpeed()} 倍速 · 松开恢复' : '',
          persistent: true,
        );
      });
    }
  }

  Future<void> applySpeed() => _setRate(baseSpeed());

  double _currentBoostSpeed() {
    if (options.autoLongPressSpeed) return baseSpeed() * 2;
    return options.longPressSpeed;
  }

  Future<void> _setRate(double value) {
    _rates = _rates
        .catchError((Object _) {})
        .then((_) => player.setRate(value));
    unawaited(
      _rates.catchError((Object _) {
        hint('倍速调整失败，请重试');
      }),
    );
    return _rates;
  }

  void _beginHold() {
    if (!available() || _holdTimer != null || _boosting) return;
    _holdTimer = Timer(const Duration(milliseconds: 350), () {
      _holdTimer = null;
      if (_disposed ||
          !available() ||
          !player.state.playing ||
          player.state.completed) {
        return;
      }
      _held = true;
      _longPressOrigin = _lastPosition;
      _speedLockArmed = false;
      HapticFeedback.lightImpact();
      if (_speedLocked) {
        _boosting = true;
        _speedLockHint = SpeedLockHint.swipeDownToUnlock;
        notifyListeners();
        return;
      }
      _boosting = true;
      unawaited(_setRate(_currentBoostSpeed()));
      _speedLockHint = SpeedLockHint.swipeUpToLock;
      notifyListeners();
      hint('${_currentBoostSpeed()} 倍速 · 上滑可锁定', persistent: true);
    });
  }

  void _endHold({bool tap = false, bool silent = false}) {
    final wasKeyboard = _keyboardHold;
    final boosted = _boosting;
    _holdTimer?.cancel();
    _holdTimer = null;
    _keyboardHold = false;
    _boosting = false;
    _longPressOrigin = null;
    final commit = _speedLockArmed;
    _speedLockArmed = false;
    if (_speedLocked) {
      if (commit) {
        _speedLocked = false;
        HapticFeedback.mediumImpact();
        unawaited(_setRate(baseSpeed()));
        _showTransientLockHint(SpeedLockHint.unlockedConfirm);
      } else {
        _speedLockHint = SpeedLockHint.none;
        notifyListeners();
      }
      hint('');
    } else if (boosted) {
      if (commit) {
        _speedLocked = true;
        HapticFeedback.mediumImpact();
        _showTransientLockHint(SpeedLockHint.lockedConfirm);
        hint('');
      } else {
        _speedLockHint = SpeedLockHint.none;
        notifyListeners();
        unawaited(_setRate(baseSpeed()));
        if (!silent) hint('恢复 ${baseSpeed()} 倍速');
      }
    }
    if (!boosted && tap && wasKeyboard) {
      seek(5);
    }
  }

  void releaseSpeedLock() {
    if (!_speedLocked) return;
    _speedLocked = false;
    _speedLockArmed = false;
    _cancelLockHintTimer();
    _speedLockHint = SpeedLockHint.none;
    notifyListeners();
    unawaited(_setRate(baseSpeed()));
  }

  void _showTransientLockHint(SpeedLockHint value) {
    _speedLockHint = value;
    notifyListeners();
    _lockHintTimer?.cancel();
    _lockHintTimer = Timer(const Duration(seconds: 2), () {
      if (_speedLockHint == value) {
        _speedLockHint = SpeedLockHint.none;
        notifyListeners();
      }
    });
  }

  void _cancelLockHintTimer() {
    _lockHintTimer?.cancel();
    _lockHintTimer = null;
  }

  void onLongPressMove(Offset localPosition) {
    if (!_boosting || _longPressOrigin == null || _viewHeight <= 0) return;
    final dy = localPosition.dy - _longPressOrigin!.dy;
    final threshold = (_viewHeight * 0.22).clamp(48.0, 112.0);
    if (_speedLocked) {
      if (!_speedLockArmed && dy >= threshold) {
        _speedLockArmed = true;
        HapticFeedback.lightImpact();
        _speedLockHint = SpeedLockHint.releaseToUnlock;
        notifyListeners();
      } else if (_speedLockArmed && dy <= threshold - 10) {
        _speedLockArmed = false;
        HapticFeedback.lightImpact();
        _speedLockHint = SpeedLockHint.swipeDownToUnlock;
        notifyListeners();
      }
    } else {
      if (!_speedLockArmed && dy <= -threshold) {
        _speedLockArmed = true;
        HapticFeedback.lightImpact();
        _speedLockHint = SpeedLockHint.releaseToLock;
        notifyListeners();
      } else if (_speedLockArmed && dy >= -(threshold - 10)) {
        _speedLockArmed = false;
        HapticFeedback.lightImpact();
        _speedLockHint = SpeedLockHint.swipeUpToLock;
        notifyListeners();
      }
    }
  }

  void cancel() {
    if (_disposed) return;
    if (_pointers.isNotEmpty) {
      _cancelUntilRelease = true;
      _ignoreTapUntil = DateTime.now().add(const Duration(milliseconds: 600));
    }
    _pointer = null;
    _origin = null;
    _lastPosition = null;
    _longPressOrigin = null;
    _endHold(silent: true);
    _hudState = const GestureHudState();
    hint('');
    notifyListeners();
  }

  void pointerDown(
    PointerDownEvent event, {
    required bool swipeEnabled,
    double width = 0.0,
    required double height,
  }) {
    _pointers.add(event.pointer);
    if (_pointers.length != 1 || _cancelUntilRelease) {
      cancel();
      return;
    }
    if (!available() || event.buttons != kPrimaryButton) return;
    _pointer = event.pointer;
    _origin = _lastPosition = event.localPosition;
    _swipeEnabled = swipeEnabled && event.kind == PointerDeviceKind.touch;
    _viewWidth = width;
    _viewHeight = height;
    _moved = _held = false;
    _swipeAction = SwipeAction.none;
    _hasToasted = false;
    _lastSeekDx = 0.0;
    _fullscreenTriggered = false;
    _brightnessGestureId++;

    _beginHold();
  }

  void pointerMove(PointerMoveEvent event) {
    if (_pointer != event.pointer || _origin == null) return;
    _lastPosition = event.localPosition;
    final diff = event.localPosition - _origin!;
    if (diff.distance > 12) {
      _moved = true;
      if (_boosting) {
        onLongPressMove(event.localPosition);
        return;
      }
      _endHold();
      onLongPressMove(event.localPosition);
    }
    if (!_swipeEnabled || !_moved || _viewHeight <= 0) return;

    final dx = diff.dx;
    final dy = diff.dy;

    if (_swipeAction == SwipeAction.none) {
      if (dx.abs() > dy.abs() * 3 && dx.abs() > 12) {
        if (player.state.duration <= Duration.zero) return;
        _swipeAction = SwipeAction.horizontal;
        _showSeekHud();
      } else if (dy.abs() > dx.abs() * 3 && dy.abs() > 12) {
        if (!options.slideVolumeBrightness && !options.slideFullscreen) {
          return;
        }
        final sectionWidth = _viewWidth / 3;
        if (event.localPosition.dx < sectionWidth) {
          if (!options.slideVolumeBrightness) return;
          _swipeAction = SwipeAction.brightness;
          _initialBrightness = _brightness;
          _showHud(SwipeAction.brightness, _brightness);
        } else if (event.localPosition.dx < sectionWidth * 2) {
          if (!options.slideFullscreen) return;
          _swipeAction = SwipeAction.fullscreen;
        } else {
          if (!options.slideVolumeBrightness) return;
          _swipeAction = SwipeAction.volume;
          _initialVolume = player.state.volume;
          _showHud(SwipeAction.volume, _initialVolume / 100);
        }
      }
      return;
    }

    switch (_swipeAction) {
      case SwipeAction.horizontal:
        final topZone = _viewHeight * 0.125;
        final inCancelZone =
            event.localPosition.dy <= topZone &&
            (event.localPosition.dx >= _viewWidth * 0.875 ||
                event.localPosition.dx <= _viewWidth * 0.125);
        if (inCancelZone) {
          if (!_hasToasted) {
            _hasToasted = true;
            _hudState = const GestureHudState(
              type: SwipeAction.horizontal,
              cancelSeek: true,
            );
            notifyListeners();
          }
          return;
        }
        if (_hasToasted) {
          _hasToasted = false;
          _showSeekHud();
        }
        _updateSeekHud(dx);
      case SwipeAction.brightness:
        final level = _viewHeight * 3;
        final value = (_initialBrightness - dy / level).clamp(0.0, 1.0);
        AppDevice.setBrightness(value);
        _brightness = value;
        _showHud(SwipeAction.brightness, value);
      case SwipeAction.volume:
        final value = (_initialVolume - dy / (_viewHeight * 0.5) * 100).clamp(
          0.0,
          100.0,
        );
        unawaited(player.setVolume(value));
        _showHud(SwipeAction.volume, value / 100);
      case SwipeAction.fullscreen:
        if (_fullscreenTriggered) break;
        const threshold = 2.5;
        final cumulative = event.localPosition.dy - _origin!.dy;
        if (cumulative > threshold) {
          _fullscreenTriggered = true;
          onFullscreen(false);
          _ignoreTapUntil = DateTime.now().add(
            const Duration(milliseconds: 600),
          );
        } else if (cumulative < -threshold) {
          _fullscreenTriggered = true;
          onFullscreen(true);
          _ignoreTapUntil = DateTime.now().add(
            const Duration(milliseconds: 600),
          );
        }
      case SwipeAction.none:
        break;
    }
  }

  Duration? _seekToPos;

  void _updateSeekHud(double dx) {
    final durationMs = player.state.duration.inMilliseconds;
    if (durationMs <= 0) return;
    final curPos =
        _seekToPos?.inMilliseconds ?? player.state.position.inMilliseconds;
    final incremental = dx - _lastSeekDx;
    _lastSeekDx = dx;
    final posDelta = (options.dragSeekSeconds * 1000 * incremental / _viewWidth)
        .round();
    final newPos = (curPos + posDelta).clamp(0, durationMs);
    _seekToPos = Duration(milliseconds: newPos);
    _showSeekHud();
  }

  void _showSeekHud() {
    final durationMs = player.state.duration.inMilliseconds;
    final target = _seekToPos ?? player.state.position;
    final delta =
        ((target.inMilliseconds - player.state.position.inMilliseconds) / 1000)
            .round();
    _hudState = GestureHudState(
      type: SwipeAction.horizontal,
      seekTarget: target,
      seekDelta: delta,
      value: durationMs > 0 ? target.inMilliseconds / durationMs : 0,
    );
    notifyListeners();
  }

  void pointerUp(PointerUpEvent event) {
    _pointers.remove(event.pointer);
    if (_cancelUntilRelease) {
      _ignoreTapUntil = DateTime.now().add(const Duration(milliseconds: 600));
      if (_pointers.isEmpty) _cancelUntilRelease = false;
      return;
    }
    if (_pointer != event.pointer || _origin == null) return;

    if (_swipeAction == SwipeAction.horizontal) {
      final target = _seekToPos;
      _seekToPos = null;
      if (target != null && !_hasToasted) {
        unawaited(onSeek(target));
        hint(
          '${target.inSeconds > player.state.position.inSeconds ? '快进至' : '后退至'} ${formatPosition(target.inSeconds.toDouble())}',
        );
      }
      _hudState = const GestureHudState();
      _ignoreTapUntil = DateTime.now().add(const Duration(milliseconds: 200));
    } else if (_swipeAction == SwipeAction.brightness ||
        _swipeAction == SwipeAction.volume) {
      _scheduleDismissHud();
    }

    if (_swipeAction == SwipeAction.brightness) {
      final gestureId = _brightnessGestureId;
      AppDevice.getBrightness()
          .then((val) {
            if (!_disposed && gestureId == _brightnessGestureId) {
              _brightness = val.clamp(0.01, 1.0);
            }
          })
          .catchError((_) {});
    }

    if (_moved || _held) {
      _ignoreTapUntil = DateTime.now().add(const Duration(milliseconds: 600));
    }
    _pointer = null;
    _origin = null;
    _endHold();
  }

  void pointerCancel(PointerCancelEvent event) {
    _pointers.remove(event.pointer);
    cancel();
    _ignoreTapUntil = DateTime.now().add(const Duration(milliseconds: 600));
    if (_pointers.isEmpty) _cancelUntilRelease = false;
  }

  void _showHud(SwipeAction action, double value) {
    if (_disposed) return;
    _hudTimer?.cancel();
    _hudState = GestureHudState(type: action, value: value);
    notifyListeners();
  }

  void _scheduleDismissHud() {
    _hudTimer?.cancel();
    _hudTimer = Timer(const Duration(milliseconds: 1000), () {
      if (_disposed) return;
      _hudState = const GestureHudState();
      notifyListeners();
    });
  }

  void seek(int seconds) {
    if (!available() || player.state.duration <= Duration.zero) return;
    _endHold();
    final target = (player.state.position.inMilliseconds + seconds * 1000)
        .clamp(0, player.state.duration.inMilliseconds);
    unawaited(onSeek(Duration(milliseconds: target)));
    hint('${seconds > 0 ? '快进至' : '后退至'} ${formatPosition(target / 1000)}');
  }

  void doubleTapSeek(bool forward) {
    if (!available() || player.state.duration <= Duration.zero) return;
    final seconds = options.doubleTapSeekSeconds;
    seek(forward ? seconds : -seconds);
  }

  void changeVolume(double delta) {
    if (!available()) return;
    final volume = (player.state.volume + delta).clamp(0.0, 100.0);
    unawaited(player.setVolume(volume));
    if (volume > 0) _unmutedVolume = volume;
    hint(volume == 0 ? '已静音' : '音量 ${volume.round()}%');
  }

  void toggleMute() {
    if (!available()) return;
    final current = player.state.volume;
    if (current > 0) _unmutedVolume = current;
    final target = current > 0 ? 0.0 : _unmutedVolume;
    unawaited(player.setVolume(target));
    hint(target == 0 ? '已静音' : '音量 ${target.round()}%');
  }

  void toggleControls() {
    _onControlsToggle?.call();
  }

  KeyEventResult key(KeyEvent event) {
    final key = event.logicalKey;
    if (event is KeyUpEvent) {
      if (key == LogicalKeyboardKey.arrowRight && _keyboardHold) {
        _endHold(tap: available());
        return KeyEventResult.handled;
      }
      return KeyEventResult.ignored;
    }
    final hardware = HardwareKeyboard.instance;
    if (hardware.isAltPressed ||
        hardware.isMetaPressed ||
        hardware.isShiftPressed) {
      cancel();
      return KeyEventResult.ignored;
    }
    if (key == LogicalKeyboardKey.f11 || key == LogicalKeyboardKey.keyF) {
      if (event is KeyDownEvent) {
        cancel();
        onFullscreen(!fullscreen);
      }
      return KeyEventResult.handled;
    }
    if (hardware.isControlPressed || !available()) {
      return KeyEventResult.ignored;
    }
    if (key != LogicalKeyboardKey.arrowRight) _endHold();
    if (key == LogicalKeyboardKey.arrowRight) {
      if (event is KeyDownEvent) {
        _keyboardHold = true;
        _beginHold();
      }
    } else if (key == LogicalKeyboardKey.arrowLeft) {
      seek(-5);
    } else if (key == LogicalKeyboardKey.arrowUp) {
      changeVolume(5);
    } else if (key == LogicalKeyboardKey.arrowDown) {
      changeVolume(-5);
    } else if (key == LogicalKeyboardKey.space ||
        key == LogicalKeyboardKey.mediaPlayPause) {
      if (event is KeyDownEvent) onTogglePlayback();
    } else if (key == LogicalKeyboardKey.keyM) {
      if (event is KeyDownEvent) toggleMute();
    } else if (key == LogicalKeyboardKey.mediaTrackNext ||
        key == LogicalKeyboardKey.mediaTrackPrevious) {
      if (event is KeyDownEvent) {
        final handler = onEpisode;
        if (handler != null) {
          hint(handler(key == LogicalKeyboardKey.mediaTrackNext ? 1 : -1));
        }
      }
    } else {
      return KeyEventResult.ignored;
    }
    return KeyEventResult.handled;
  }

  @override
  void dispose() {
    _disposed = true;
    _holdTimer?.cancel();
    _hintTimer?.cancel();
    _hudTimer?.cancel();
    _lockHintTimer?.cancel();
    AppDevice.resetBrightness();
    if (_boosting || _speedLocked) unawaited(_setRate(baseSpeed()));
    _boosting = false;
    _speedLocked = false;
    _playing.cancel();
    _pointers.clear();
    super.dispose();
  }
}
