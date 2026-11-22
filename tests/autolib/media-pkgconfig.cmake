cmake_minimum_required(VERSION 3.20)

execute_process(COMMAND "${CMAKE_COMMAND}" -E env
    "PKG_CONFIG_PATH=${GSTREAMER}/lib/pkgconfig:${FFMPEG}/lib/pkgconfig"
    "${PKG_CONFIG}" --cflags --libs gstreamer-app-1.0 gstreamer-video-1.0 gstreamer-rtsp-server-1.0 orc-0.4
    libavcodec libavformat libavutil libavfilter
    OUTPUT_VARIABLE flags OUTPUT_STRIP_TRAILING_WHITESPACE COMMAND_ERROR_IS_FATAL ANY)
# pkg-config 的输出包含 shell 转义，必须保留单个参数里的路径空格。
separate_arguments(flags UNIX_COMMAND "${flags}")
execute_process(COMMAND "${CC}" "${CMAKE_CURRENT_LIST_DIR}/media-pkgconfig.c" -o "${OUTPUT}" ${flags}
    COMMAND_ERROR_IS_FATAL ANY)
execute_process(COMMAND "${GSTREAMER}/bin/gst-run" "${OUTPUT}" COMMAND_ERROR_IS_FATAL ANY)
