import 'package:flutter/widgets.dart';

import 'file_video_stub.dart'
    if (dart.library.js_interop) 'file_video_web.dart'
    as impl;

/// A native browser video player for a progressive MP4 URL.
Widget fileVideoView(String url) => impl.fileVideoView(url);
