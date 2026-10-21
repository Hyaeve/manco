#!/bin/sh
set -eu

cd /build
curl -fsSL --retry 3 \
    https://codeload.github.com/FFmpeg/FFmpeg/tar.gz/refs/tags/n6.1.2 \
    -o ffmpeg.tar.gz
echo "9cbefd67b7e4fffde3846699ec03324b7ef789a81c2e9b1059e93719b5b8d189  ffmpeg.tar.gz" | sha256sum -c -
tar -xzf ffmpeg.tar.gz
cd FFmpeg-n6.1.2

# Manco copies streams to Matroska; it does not transcode or render video.
# Keep native probing decoders, manifest readers, TLS and AES-HLS support.
./configure \
    --prefix=/out \
    --disable-autodetect --disable-debug --disable-doc --enable-small \
    --disable-shared --enable-static --disable-everything \
    --disable-programs --enable-ffmpeg --disable-avdevice --disable-postproc \
    --enable-demuxers --enable-parsers --enable-bsfs \
    --enable-protocol=file,http,https,httpproxy,tcp,tls,crypto,data \
    --enable-muxer=matroska \
    --enable-decoder=h264,hevc,av1,vp8,vp9,mpeg2video,mpeg4,aac,mp3,opus,vorbis,ac3,eac3 \
    --enable-openssl --enable-libxml2 --enable-zlib --enable-version3
make -j2
make install
mkdir -p /out/licenses
cp COPYING.LGPLv3 LICENSE.md /out/licenses/
cp ffbuild/config.mak /out/licenses/build-config.txt

mkdir -p /tmp/hls /tmp/dash
cd /tmp/hls
/usr/bin/ffmpeg -nostdin -hide_banner -loglevel error -f lavfi \
    -i color=c=black:s=64x64:r=10 -t 2 -c:v mpeg2video \
    -hls_time 1 -hls_list_size 0 -f hls media.m3u8
/out/bin/ffmpeg -nostdin -hide_banner -loglevel error \
    -i /tmp/hls/media.m3u8 -map 0 -c copy -f matroska /tmp/hls/check.mkv
cd /tmp/dash
/usr/bin/ffmpeg -nostdin -hide_banner -loglevel error -f lavfi \
    -i color=c=black:s=64x64:r=10 -t 2 -c:v mpeg4 -f dash media.mpd
/out/bin/ffmpeg -nostdin -hide_banner -loglevel error \
    -i /tmp/dash/media.mpd -map 0 -c copy -f matroska /tmp/dash/check.mkv
test -s /tmp/hls/check.mkv
test -s /tmp/dash/check.mkv
