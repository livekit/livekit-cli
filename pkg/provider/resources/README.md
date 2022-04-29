## Static videos

Sources:
* [butterfly](https://pixabay.com/videos/butterfly-wing-clap-loop-color-6887/)
* [cartoon](https://pixabay.com/videos/kids-cartoon-background-landscape-26796/)
* [circles](https://pixabay.com/videos/circles-tunnel-neon-glow-abstract-53209/)
* [crescent](https://pixabay.com/videos/crescent-moon-clouds-night-kids-27186/)
* [neon](https://pixabay.com/videos/neon-terrain-80-retro-abstract-21368/)
* [tunnel](https://pixabay.com/videos/tunnel-4k-uhd-60fps-65771/)

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
