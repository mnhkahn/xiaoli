package esp32

import (
	"sync"
	"time"
)

type VoiceDetector interface {
	Detect(payload []byte) (isVoice bool, ran bool, probability float32)
	Close()
}

type VoiceAppendStats struct {
	Received     int
	Voice        int
	Buffered     int
	VADRan       bool
	VADIsVoice   bool
	VADProb      float32
	Accepted     bool
	LimitReached bool
}

type VoiceSnapshot struct {
	Listening         bool
	ListenMode        string
	HasVoice          bool
	LastVoiceAt       time.Time
	LastVoiceActivity time.Time
}

type VoiceRecorder struct {
	mu                sync.Mutex
	listening         bool
	listenMode        string
	audioFrames       [][]byte
	voiceRunning      bool
	lastVoiceAt       time.Time
	lastVoiceActivity time.Time
	hasVoice          bool
	audioRecvCnt      int
	audioVoiceCnt     int
	detector          VoiceDetector
}

func NewVoiceRecorder() *VoiceRecorder {
	return &VoiceRecorder{lastVoiceActivity: time.Now()}
}

func (r *VoiceRecorder) Start(mode string, detector VoiceDetector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listening = true
	r.listenMode = mode
	r.audioFrames = nil
	r.lastVoiceAt = time.Time{}
	r.lastVoiceActivity = time.Now()
	r.hasVoice = false
	r.audioRecvCnt = 0
	r.audioVoiceCnt = 0
	if r.detector == nil {
		r.detector = detector
	}
}

func (r *VoiceRecorder) HasDetector() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.detector != nil
}

func (r *VoiceRecorder) Touch() {
	r.mu.Lock()
	r.lastVoiceActivity = time.Now()
	r.mu.Unlock()
}

func (r *VoiceRecorder) Append(payload []byte) (recv, voice, total int) {
	stats := r.AppendStats(payload)
	return stats.Received, stats.Voice, stats.Buffered
}

func (r *VoiceRecorder) AppendStats(payload []byte) VoiceAppendStats {
	if len(payload) == 0 {
		return VoiceAppendStats{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.listening {
		return VoiceAppendStats{}
	}
	if len(r.audioFrames) >= 400 {
		return VoiceAppendStats{Received: r.audioRecvCnt, Voice: r.audioVoiceCnt, Buffered: len(r.audioFrames), LimitReached: true}
	}
	r.audioRecvCnt++
	stats := VoiceAppendStats{Received: r.audioRecvCnt, Accepted: true}
	now := time.Now()
	if r.detector != nil {
		isVoice, ran, prob := r.detector.Detect(payload)
		stats.VADRan = ran
		stats.VADIsVoice = isVoice
		stats.VADProb = prob
		if isVoice {
			r.lastVoiceAt = now
			r.lastVoiceActivity = now
			r.hasVoice = true
			r.audioVoiceCnt++
		}
	} else {
		r.lastVoiceAt = now
		r.lastVoiceActivity = now
		r.hasVoice = true
		r.audioVoiceCnt++
	}
	frame := append([]byte(nil), payload...)
	r.audioFrames = append(r.audioFrames, frame)
	stats.Voice = r.audioVoiceCnt
	stats.Buffered = len(r.audioFrames)
	return stats
}

func (r *VoiceRecorder) Snapshot() VoiceSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return VoiceSnapshot{
		Listening:         r.listening,
		ListenMode:        r.listenMode,
		HasVoice:          r.hasVoice,
		LastVoiceAt:       r.lastVoiceAt,
		LastVoiceActivity: r.lastVoiceActivity,
	}
}

func (r *VoiceRecorder) Stop() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.listening && len(r.audioFrames) == 0 {
		return nil
	}
	r.listening = false
	frames := make([][]byte, len(r.audioFrames))
	for i := range r.audioFrames {
		frames[i] = append([]byte(nil), r.audioFrames[i]...)
	}
	r.audioFrames = nil
	return frames
}

func (r *VoiceRecorder) TryStartProcessing() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.voiceRunning {
		return false
	}
	r.voiceRunning = true
	return true
}

func (r *VoiceRecorder) FinishProcessing() {
	r.mu.Lock()
	r.voiceRunning = false
	r.mu.Unlock()
}

func (r *VoiceRecorder) Close() {
	r.mu.Lock()
	detector := r.detector
	r.detector = nil
	r.mu.Unlock()
	if detector != nil {
		detector.Close()
	}
}
