package jitter

import (
	"math"
	"sync"
	"time"
)

const (
	maxQueuedPackets = 64
	maxSampleQueue   = 48000 // 1 second hard cap @ 48kHz
	minResampleRate  = 0.996
	maxResampleRate  = 1.004
)

type Stats struct {
	BufferedMs      float64
	TargetMs        float64
	JitterMs        float64
	LossPercent     float64
	DriftPercent    float64
	PacketsReceived uint64
	PacketsLost     uint64
	Underruns       uint64
	Overruns        uint64
}

// Buffer provides sequence-ordered audio jitter buffering with packet loss concealment
// and adaptive clock-drift resampling.
type Buffer struct {
	mu           sync.Mutex
	sampleRate   int
	targetMs     float64
	initialized  bool
	nextSeq      uint32
	packets      map[uint32][]float32
	samples      []float32
	readPos      float64
	lastSample   float32
	primed       bool
	resampleRate float64
	ewmaFill     float64

	// Network stats
	lastArrival  time.Time
	lastTransit  float64
	jitterUs     float64
	pktRecv      uint64
	pktLost      uint64
	underruns    uint64
	overruns     uint64
	lastFrameLen int
}

func NewBuffer(sampleRate int, targetMs int) *Buffer {
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if targetMs < 10 {
		targetMs = 10
	}
	if targetMs > 150 {
		targetMs = 150
	}
	return &Buffer{
		sampleRate:   sampleRate,
		targetMs:     float64(targetMs),
		packets:      make(map[uint32][]float32, 32),
		samples:      make([]float32, 0, sampleRate/4),
		resampleRate: 1.0,
		ewmaFill:     float64(sampleRate) * float64(targetMs) / 1000.0,
		lastFrameLen: 480,
	}
}

func (b *Buffer) SetTargetMs(ms int) {
	if ms < 10 {
		ms = 10
	}
	if ms > 150 {
		ms = 150
	}
	b.mu.Lock()
	b.targetMs = float64(ms)
	b.mu.Unlock()
}

func (b *Buffer) Reset() {
	b.mu.Lock()
	b.initialized = false
	b.primed = false
	clear(b.packets)
	b.samples = b.samples[:0]
	b.readPos = 0
	b.lastSample = 0
	b.resampleRate = 1.0
	b.ewmaFill = float64(b.sampleRate) * b.targetMs / 1000.0
	b.lastArrival = time.Time{}
	b.jitterUs = 0
	b.pktRecv = 0
	b.pktLost = 0
	b.underruns = 0
	b.overruns = 0
	b.mu.Unlock()
}

// seqDiff returns signed difference a - b handling uint32 wrap-around.
func seqDiff(a, b uint32) int32 {
	return int32(a - b)
}

// Push inserts a decoded audio frame with its sequence number and sender timestamp (microseconds).
func (b *Buffer) Push(seq uint32, timestampUs uint64, pcm []float32) {
	if len(pcm) == 0 {
		return
	}
	now := time.Now()

	b.mu.Lock()
	defer b.mu.Unlock()

	b.pktRecv++
	b.lastFrameLen = len(pcm)

	// RFC 3550 style interarrival jitter estimation
	if !b.lastArrival.IsZero() {
		arrivalUs := float64(now.Sub(b.lastArrival).Microseconds())
		expectedUs := float64(len(pcm)) * 1_000_000.0 / float64(b.sampleRate)
		d := math.Abs(arrivalUs - expectedUs)
		b.jitterUs += (d - b.jitterUs) / 16.0
	}
	b.lastArrival = now
	_ = timestampUs

	if !b.initialized {
		b.initialized = true
		b.nextSeq = seq
	}

	diff := seqDiff(seq, b.nextSeq)
	if diff < 0 {
		// Too late / already consumed. If huge jump (sender restarted), resync.
		if diff < -100 {
			clear(b.packets)
			b.nextSeq = seq
		} else {
			return
		}
	} else if diff > maxQueuedPackets {
		// Huge forward jump (sender restarted or long network outage): resync.
		clear(b.packets)
		b.nextSeq = seq
	}

	// Copy PCM slice so caller can reuse its buffer
	cp := make([]float32, len(pcm))
	copy(cp, pcm)
	b.packets[seq] = cp

	// Drain consecutive packets into sample queue
	b.drainPacketsLocked()
}

func (b *Buffer) drainPacketsLocked() {
	for {
		frame, ok := b.packets[b.nextSeq]
		if !ok {
			// If we have future packets waiting beyond 3 frames, treat missing nextSeq as lost.
			if len(b.packets) >= 3 {
				b.pktLost++
				b.appendConcealmentLocked(b.lastFrameLen)
				b.nextSeq++
				continue
			}
			break
		}
		delete(b.packets, b.nextSeq)
		b.nextSeq++
		b.appendSamplesLocked(frame)
	}
}

func (b *Buffer) appendSamplesLocked(frame []float32) {
	b.samples = append(b.samples, frame...)
	if len(frame) > 0 {
		b.lastSample = frame[len(frame)-1]
	}
	// Hard cap to avoid unbounded latency on burst
	maxAllowed := int(float64(b.sampleRate)*b.targetMs/1000.0)*3 + b.lastFrameLen*2
	if maxAllowed < b.sampleRate/5 {
		maxAllowed = b.sampleRate / 5
	}
	if maxAllowed > maxSampleQueue {
		maxAllowed = maxSampleQueue
	}
	avail := len(b.samples) - int(b.readPos)
	if avail > maxAllowed {
		drop := avail - int(float64(b.sampleRate)*b.targetMs/1000.0)
		start := int(b.readPos) + drop
		if drop > 0 && start < len(b.samples) {
			b.samples = append(b.samples[:0], b.samples[start:]...)
			b.readPos = 0
			b.overruns++
		}
	}
}

func (b *Buffer) appendConcealmentLocked(n int) {
	if n <= 0 {
		n = 480
	}
	s := b.lastSample
	for i := 0; i < n; i++ {
		s *= 0.96 // smooth fade to silence to avoid clicks
		b.samples = append(b.samples, s)
	}
	b.lastSample = s
}

// Read fills dst with audio samples, applying adaptive clock-drift resampling.
func (b *Buffer) Read(dst []float32) {
	b.mu.Lock()
	defer b.mu.Unlock()

	targetSamples := float64(b.sampleRate) * b.targetMs / 1000.0
	avail := float64(len(b.samples)) - b.readPos

	// Wait until buffer reaches target before starting playback
	if !b.primed {
		if avail < targetSamples {
			for i := range dst {
				dst[i] = 0
			}
			return
		}
		b.primed = true
		b.ewmaFill = avail
	}

	// Update EWMA fill level and adjust clock-drift resampling rate
	b.ewmaFill = 0.98*b.ewmaFill + 0.02*avail
	deadband := float64(b.sampleRate) * 0.008 // +/- 8ms deadband
	errSamples := b.ewmaFill - targetSamples
	if errSamples > deadband {
		// Buffer growing -> play slightly faster
		adj := (errSamples - deadband) / (float64(b.sampleRate) * 0.5)
		b.resampleRate = 1.0 + math.Min(adj, maxResampleRate-1.0)
	} else if errSamples < -deadband {
		// Buffer shrinking -> play slightly slower
		adj := (-errSamples - deadband) / (float64(b.sampleRate) * 0.5)
		b.resampleRate = 1.0 - math.Min(adj, 1.0-minResampleRate)
	} else {
		// Gently return toward 1.0
		b.resampleRate = 0.99*b.resampleRate + 0.01*1.0
	}

	// Linear interpolation readout
	for i := range dst {
		idx := int(b.readPos)
		if idx+1 >= len(b.samples) {
			// Underrun: fade smoothly to silence and re-prime
			b.lastSample *= 0.94
			dst[i] = b.lastSample
			if i == len(dst)-1 {
				b.underruns++
				b.primed = false
			}
			continue
		}
		frac := float32(b.readPos - float64(idx))
		s0 := b.samples[idx]
		s1 := b.samples[idx+1]
		out := s0 + (s1-s0)*frac
		dst[i] = out
		b.lastSample = out
		b.readPos += b.resampleRate
	}

	// Compact consumed prefix
	consumed := int(b.readPos)
	if consumed > 0 {
		if consumed >= len(b.samples) {
			b.samples = b.samples[:0]
			b.readPos = 0
		} else {
			copy(b.samples, b.samples[consumed:])
			b.samples = b.samples[:len(b.samples)-consumed]
			b.readPos -= float64(consumed)
		}
	}
}

func (b *Buffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	avail := float64(len(b.samples)) - b.readPos
	if avail < 0 {
		avail = 0
	}
	total := b.pktRecv + b.pktLost
	var lossPct float64
	if total > 0 {
		lossPct = float64(b.pktLost) * 100.0 / float64(total)
	}
	return Stats{
		BufferedMs:      avail * 1000.0 / float64(b.sampleRate),
		TargetMs:        b.targetMs,
		JitterMs:        b.jitterUs / 1000.0,
		LossPercent:     lossPct,
		DriftPercent:    (b.resampleRate - 1.0) * 100.0,
		PacketsReceived: b.pktRecv,
		PacketsLost:     b.pktLost,
		Underruns:       b.underruns,
		Overruns:        b.overruns,
	}
}
