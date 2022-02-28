## Static videos

Source: https://pixabay.com/videos/butterfly-wing-clap-loop-color-6887/
License: [Pixabay license](https://pixabay.com/service/license/)

Encoding parameters

```shell
ffmpeg -i butterfly_original.mp4 \
  -c:v libx264 -bsf:v h264_mp4toannexb \
  -b:v 2M -vf "scale=1280:720, fps=30" \
  -x264-params keyint=120 -max_delay 0 -bf 0 \
  butterfly_720_2000.h264
```
