import 'media_platform.dart';
import 'media_factory_stub.dart'
    if (dart.library.js_interop) 'media_factory_web.dart'
    as implementation;

MediaPlatform createMediaPlatform() => implementation.createMediaPlatform();
