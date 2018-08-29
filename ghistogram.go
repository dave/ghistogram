//  Copyright (c) 2015 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the
//  License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing,
//  software distributed under the License is distributed on an "AS
//  IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
//  express or implied. See the License for the specific language
//  governing permissions and limitations under the License.

// Package ghistogram provides a simple histogram of uint64's that
// avoids heap allocations (garbage creation) during data processing.
package ghistogram

import (
	"bytes"
	"fmt"
	"math"
	"sync"
)

// Histogram is a simple uint64 histogram implementation that avoids
// heap allocations during its processing of incoming data points.
//
// It was motivated for tracking simple performance timings.
//
// The histogram bins are split across the two arrays of Ranges and
// Counts, where len(Ranges) == len(Counts).  These arrays are public
// in case users wish to use reflection or JSON marhsaling.
//
// An optional growth factor for bin sizes is supported - see
// NewHistogram() binGrowthFactor parameter.
//
// The histogram is concurrent safe.
type Histogram struct {
	// Ranges holds the lower domain bounds of bins, so bin i has data
	// point domain of "[Ranges[i], Ranges[i+1])".  Related,
	// Ranges[0] == 0 and Ranges[1] == binFirst.
	Ranges []uint64

	// Counts holds the event counts for bins.
	Counts []uint64

	// TotCount is the sum of all counts.
	TotCount uint64

	TotDataPoint uint64 // TotDataPoint is the sum of all data points.
	MinDataPoint uint64 // MinDataPoint is the smallest data point seen.
	MaxDataPoint uint64 // MaxDataPoint is the largest data point seen.

	m sync.Mutex
}

// NewHistogram creates a new, ready to use Histogram.  The numBins
// must be >= 2.  The binFirst is the width of the first bin.  The
// binGrowthFactor must be > 1.0 or 0.0.
//
// A special case of binGrowthFactor of 0.0 means the the allocated
// bins will have constant, non-growing size or "width".
func NewHistogram(
	numBins int,
	binFirst uint64,
	binGrowthFactor float64) *Histogram {
	gh := &Histogram{
		Ranges:       make([]uint64, numBins),
		Counts:       make([]uint64, numBins),
		TotCount:     0,
		MinDataPoint: math.MaxUint64,
		MaxDataPoint: 0,
	}

	gh.Ranges[0] = 0
	gh.Ranges[1] = binFirst

	for i := 2; i < len(gh.Ranges); i++ {
		if binGrowthFactor == 0.0 {
			gh.Ranges[i] = gh.Ranges[i-1] + binFirst
		} else {
			gh.Ranges[i] =
				uint64(math.Ceil(binGrowthFactor * float64(gh.Ranges[i-1])))
		}
	}

	return gh
}

// Add increases the count in the bin for the given dataPoint.
func (gh *Histogram) Add(dataPoint uint64, count uint64) {
	gh.m.Lock()

	idx := search(gh.Ranges, dataPoint)
	if idx >= 0 {
		gh.Counts[idx] += count
		gh.TotCount += count

		gh.TotDataPoint += dataPoint
		if gh.MinDataPoint > dataPoint {
			gh.MinDataPoint = dataPoint
		}
		if gh.MaxDataPoint < dataPoint {
			gh.MaxDataPoint = dataPoint
		}
	}

	gh.m.Unlock()
}

// Finds the last arr index where the arr entry <= dataPoint.
func search(arr []uint64, dataPoint uint64) int {
	i, j := 0, len(arr)

	for i < j {
		h := i + (j-i)/2 // Avoids h overflow, where i <= h < j.
		if dataPoint >= arr[h] {
			i = h + 1
		} else {
			j = h
		}
	}

	return i - 1
}

// AddAll adds all the Counts from the src histogram into this
// histogram.  The src and this histogram must either have the same
// exact creation parameters.
func (gh *Histogram) AddAll(src *Histogram) {
	src.m.Lock()
	gh.m.Lock()

	for i := 0; i < len(src.Counts); i++ {
		gh.Counts[i] += src.Counts[i]
	}
	gh.TotCount += src.TotCount

	gh.TotDataPoint += src.TotDataPoint
	if gh.MinDataPoint > src.MinDataPoint {
		gh.MinDataPoint = src.MinDataPoint
	}
	if gh.MaxDataPoint < src.MaxDataPoint {
		gh.MaxDataPoint = src.MaxDataPoint
	}

	gh.m.Unlock()
	src.m.Unlock()
}

// Graph emits an ascii graph to the optional out buffer, allocating a
// out buffer if none was supplied.  Returns the out buffer.  Each
// line emitted may have an optional prefix.
//
// For example:
//       0+  10=2 10.00% ********
//      10+  10=1 10.00% ****
//      20+  10=3 10.00% ************
func (gh *Histogram) EmitGraph(prefix []byte,
	out *bytes.Buffer) *bytes.Buffer {
	gh.m.Lock()

	ranges := gh.Ranges
	//rangesN := len(ranges)
	counts := gh.Counts
	countsN := len(counts)

	var rangeLabels []string
	for i := range ranges {
		if i == len(ranges)-1 {
			rangeLabels = append(rangeLabels, fmt.Sprintf("%d+", ranges[i]))
		} else {
			rangeLabels = append(rangeLabels, fmt.Sprintf("%d - %d", ranges[i], ranges[i+1]))
		}
	}

	if out == nil {
		out = bytes.NewBuffer(make([]byte, 0, 80*countsN))
	}

	var maxCount uint64
	for _, c := range counts {
		if maxCount < c {
			maxCount = c
		}
	}
	maxCountF := float64(maxCount)

	var runCount uint64 // Running total while emitting lines.

	barLen := float64(len(bar))

	for i, c := range counts {
		if prefix != nil {
			out.Write(prefix)
		}
		runCount += c

		if i == len(ranges)-1 {
			fmt.Fprintf(out, "%4d+      ", ranges[i])
		} else {
			fmt.Fprintf(out, "%4d - %-4d", ranges[i], ranges[i+1])
		}

		if c > 0 {
			out.Write([]byte(" "))
			barWant := int(math.Floor(barLen * (float64(c) / maxCountF)))
			out.Write(bar[0:barWant])
		}

		out.Write([]byte("\n"))
	}

	gh.m.Unlock()

	return out
}

var bar = []byte("******************************")

// CallSync invokes the callback func while the histogram is locked.
func (gh *Histogram) CallSync(f func()) {
	gh.m.Lock()
	f()
	gh.m.Unlock()
}
