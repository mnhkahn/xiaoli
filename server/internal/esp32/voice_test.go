package esp32

import "testing"

func TestVoiceRecorderStartAppendStopCopiesFrames(t *testing.T) {
	rec := NewVoiceRecorder()
	rec.Start("manual", nil)

	frame := []byte{1, 2, 3}
	recv, voice, total := rec.Append(frame)
	if recv != 1 || voice != 1 || total != 1 {
		t.Fatalf("Append() = %d,%d,%d, want 1,1,1", recv, voice, total)
	}
	frame[0] = 9

	frames := rec.Stop()
	if len(frames) != 1 || frames[0][0] != 1 {
		t.Fatalf("Stop() frames = %#v, want copied original frame", frames)
	}
	if got := rec.Stop(); got != nil {
		t.Fatalf("second Stop() = %#v, want nil", got)
	}
}

func TestVoiceRecorderProcessingLock(t *testing.T) {
	rec := NewVoiceRecorder()
	if !rec.TryStartProcessing() {
		t.Fatal("first TryStartProcessing() = false, want true")
	}
	if rec.TryStartProcessing() {
		t.Fatal("second TryStartProcessing() = true, want false")
	}
	rec.FinishProcessing()
	if !rec.TryStartProcessing() {
		t.Fatal("TryStartProcessing() after finish = false, want true")
	}
}
