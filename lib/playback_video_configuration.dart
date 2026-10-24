import 'dart:io';

import 'package:media_kit_video/media_kit_video.dart';

VideoControllerConfiguration playbackVideoConfiguration() =>
    VideoControllerConfiguration(
      enableHardwareAcceleration: !Platform.isIOS,
      hwdec: Platform.isWindows ? 'auto-copy' : null,
    );
