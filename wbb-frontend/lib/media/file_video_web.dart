import 'dart:ui_web' as ui_web;

import 'package:flutter/widgets.dart';
import 'package:web/web.dart' as web;

int _next = 0;

Widget fileVideoView(String url) {
  final viewType = 'wbb-file-video-${_next++}';
  ui_web.platformViewRegistry.registerViewFactory(viewType, (int _) {
    final video = web.HTMLVideoElement()
      ..src = url
      ..controls = true
      ..playsInline = true
      ..preload = 'metadata';
    video.style
      ..width = '100%'
      ..height = '100%'
      ..backgroundColor = '#000';
    return video;
  });
  return HtmlElementView(viewType: viewType);
}
