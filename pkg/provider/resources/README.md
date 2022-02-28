## Static videos

Source: https://pixabay.com/videos/butterfly-wing-clap-loop-color-6887/
License: [Pixabay license](https://pixabay.com/service/license/)

Encoding command

```shell
ffmpeg -i butterfly.mp4 \
  -c:v libx264 -bsf:v h264_mp4toannexb \
  -b:v 3M -vf "scale=1920:1080, fps=30" \
  -profile baseline -pix_fmt yuv420p \
  -x264-params keyint=120 -max_delay 0 -bf 0 \
  butterfly_1080_3000.h264

ffmpeg -i butterfly.mp4 \
  -c:v libx264 -bsf:v h264_mp4toannexb \
  -b:v 2M -vf "scale=1280:720, fps=30" \
  -profile baseline -pix_fmt yuv420p \
  -x264-params keyint=120 -max_delay 0 -bf 0 \
  butterfly_720_2000.h264

ffmpeg -i butterfly.mp4 \
  -c:v libx264 -bsf:v h264_mp4toannexb \
  -b:v 800K -vf "scale=960:540, fps=25" \
  -profile baseline -pix_fmt yuv420p \
  -x264-params keyint=120 -max_delay 0 -bf 0 \
  butterfly_540_800.h264

ffmpeg -i butterfly.mp4 \
  -c:v libx264 -bsf:v h264_mp4toannexb \
  -b:v 400K -vf "scale=640:360, fps=20" \
  -profile baseline -pix_fmt yuv420p \
  -x264-params keyint=120 -max_delay 0 -bf 0 \
  butterfly_360_400.h264

ffmpeg -i butterfly.mp4 \
  -c:v libx264 -bsf:v h264_mp4toannexb \
  -b:v 150K -vf "scale=320:180, fps=15" \
  -profile baseline -pix_fmt yuv420p \
  -x264-params keyint=120 -max_delay 0 -bf 0 \
  butterfly_180_150.h264
```
