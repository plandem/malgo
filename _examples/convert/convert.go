package main

import (
	"encoding/binary"
	"fmt"
	"github.com/gen2brain/malgo"
	"github.com/youpy/go-riff"
	"github.com/youpy/go-wav"
	"io"
	"os"
)

type Writer struct {
	file         *os.File
	dataSize     int
	channels     int
	sampleRate   int
	bitDepth     int
	dataChunkPos int64
	format       wav.WavFormat
}

// NewWriter creates a new WAV writer for streaming audio
func NewWriter(filename string, format WavFormat) (*Writer, error) {
	file, err := os.Create(filename)
	if err != nil {
		return nil, err
	}

	// Create a RIFF writer with placeholder size
	riffWriter := riff.NewWriter(file, []byte("WAVE"), 0)

	// Create WAV format chunk
	format.BlockAlign = format.NumChannels * format.BitsPerSample / 8
	format.ByteRate = format.SampleRate * uint32(format.BlockAlign)

	// Write format chunk
	err = riffWriter.WriteChunk([]byte("fmt "), 16, func(w io.Writer) {
		binary.Write(w, binary.LittleEndian, format)
	})
	if err != nil {
		file.Close()
		return nil, err
	}

	// Write data chunk header with placeholder size
	_, err = io.WriteString(file, "data")
	if err != nil {
		file.Close()
		return nil, err
	}

	// Remember position where we need to write the data size later
	dataChunkPos, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		file.Close()
		return nil, err
	}

	// Write a placeholder size (0)
	err = binary.Write(file, binary.LittleEndian, uint32(0))
	if err != nil {
		file.Close()
		return nil, err
	}

	return &Writer{
		file:         file,
		dataSize:     0,
		format:       format,
		dataChunkPos: dataChunkPos,
	}, nil
}

// Write implements io.Writer
func (w *Writer) Write(p []byte) (n int, err error) {
	n, err = w.file.Write(p)
	w.dataSize += n
	return
}

// Close finalizes the WAV file by updating headers with correct sizes
func (w *Writer) Close() error {
	// Go back to data chunk size position and update it
	_, err := w.file.Seek(w.dataChunkPos, io.SeekStart)
	if err != nil {
		return err
	}

	// Write the actual data size
	err = binary.Write(w.file, binary.LittleEndian, uint32(w.dataSize))
	if err != nil {
		return err
	}

	// Go to beginning of file to update the RIFF chunk size
	_, err = w.file.Seek(4, io.SeekStart)
	if err != nil {
		return err
	}

	// RIFF chunk size is: 4 (WAVE) + 8 (fmt chunk header) + 16 (fmt chunk) + 8 (data chunk header) + dataSize
	riffSize := uint32(4 + 8 + 16 + 8 + w.dataSize)
	err = binary.Write(w.file, binary.LittleEndian, riffSize)
	if err != nil {
		return err
	}

	return w.file.Close()
}

func BitsToType(bits int) malgo.FormatType {
	switch bits {
	case 8:
		return malgo.FormatU8
	case 16:
		return malgo.FormatS16
	case 24:
		return malgo.FormatS24
	case 32:
		return malgo.FormatS32
	default:
		return malgo.FormatUnknown
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("No input wav file.")
		os.Exit(1)
	}

	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	defer file.Close()

	w := wav.NewReader(file)
	inputFormat, err := w.Format()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	outputFormat := wav.WavFormat{
		AudioFormat:   wav.AudioFormatPCM,
		NumChannels:   1,
		SampleRate:    48000,
		BitsPerSample: 32,
	}

	wavWriter, err := NewWriter("converted.wav", outputFormat)
	if err != nil {
		fmt.Println("Failed to create WAV file:", err)
		os.Exit(1)
	}
	defer wavWriter.Close()

	formatTypeIn := BitsToType(int(inputFormat.BitsPerSample))
	if inputFormat.AudioFormat == wav.AudioFormatIEEEFloat {
		formatTypeIn = malgo.FormatF32
	}

	formatTypeOut := BitsToType(outputFormat.BitsPerSample)
	if outputFormat.AudioFormat == wav.AudioFormatIEEEFloat {
		formatTypeOut = malgo.FormatF32
	}

	config := malgo.ConverterConfig{
		FormatIn:      formatTypeIn,
		FormatOut:     formatTypeOut,
		ChannelsIn:    inputFormat.NumChannels,
		ChannelsOut:   outputFormat.NumChannels,
		SampleRateIn:  inputFormat.SampleRate,
		SampleRateOut: outputFormat.SampleRate,
		Resampling: malgo.ResampleConfig{
			Algorithm: malgo.ResampleAlgorithmLinear,
		},
		DitherMode:     malgo.DitherModeTriangle,
		ChannelMixMode: malgo.ChannelMixModeSimple,
	}
	converter, err := malgo.InitConverter(config)
	if err != nil {
		fmt.Print(err)
		os.Exit(-1)
	}

	inFrameSize := malgo.FrameSizeInBytes(config.FormatIn, config.ChannelsIn)
	outFrameSize := malgo.FrameSizeInBytes(config.FormatOut, config.ChannelsOut)

	inputFrames := 1000
	expectFrames, _ := converter.ExpectOutputFrameCount(inputFrames)
	inBuffer := make([]byte, inFrameSize*inputFrames)
	outBuffer := make([]byte, outFrameSize*expectFrames)

	for {
		n, err := w.Read(inBuffer)
		if err != nil {
			break
		}

		readFrameCount := n / inFrameSize
		_, outFrameCount, err := converter.ProcessFrames(inBuffer, readFrameCount, outBuffer, expectFrames)
		if err != nil {
			fmt.Print(err)
			continue
		} else {
			wavWriter.Write(outBuffer[:outFrameCount*outFrameSize])
		}
	}

	converter.Uninit()
}
