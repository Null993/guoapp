import argparse
import subprocess
from pathlib import Path


def main():
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', type=Path, default=root / 'build' / 'windows-fixes' / 'edge-media')
    output = parser.parse_args().output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    fixtures = [
        ('edge-540x960', 540, 960, 'libx264', 'yuv420p'),
        ('edge-1080x1920', 1080, 1920, 'libx264', 'yuv420p'),
        ('edge-1920x1080', 1920, 1080, 'libx264', 'yuv420p'),
        ('edge-1080x1920-hevc8', 1080, 1920, 'libx265', 'yuv420p'),
        ('edge-1080x1920-hevc10', 1080, 1920, 'libx265', 'yuv420p10le'),
    ]
    for name, width, height, encoder, pixel_format in fixtures:
        options = ['-threads', '2'] if encoder == 'libx264' else [
            '-preset', 'ultrafast', '-x265-params', 'log-level=error:pools=2:frame-threads=1',
        ]
        subprocess.run([
            'ffmpeg', '-v', 'error', '-y', '-f', 'lavfi', '-i',
            f'color=c=gray:s={width}x{height}:r=12', '-vf',
            'drawbox=x=0:y=0:w=18:h=ih:c=red:t=fill,drawbox=x=iw-18:y=0:w=18:h=ih:c=blue:t=fill',
            '-t', '12', '-c:v', encoder, *options, '-pix_fmt', pixel_format,
            '-movflags', '+faststart', str(output / f'{name}.mp4'),
        ], check=True)
    print(output)


if __name__ == '__main__':
    main()
