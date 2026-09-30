# C3 cartoon faces

The three expression sheets were generated from a user-provided photo. The original photo is not stored in this repository. Each sheet uses the same character, with only the head visible on a dark navy background.

Run `python3 build_faces.py` from any directory to regenerate the 24 `assets/face_*.rgb565` frames. Pillow is required only for this build-time conversion. Each frame is 168×200 RGB565, little-endian, and is read directly from the mapped assets partition. The first 21 names match the firmware's existing emotion protocol; `listening`, `speaking`, and `blink` are C3-specific states.

The ST7789 is a backlit LCD. The dark background makes the face easier to read, while backlight brightness remains the main display power control.
