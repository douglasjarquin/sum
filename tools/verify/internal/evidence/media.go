package evidence

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/proc"
	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/value"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

const videoFPS = 10

// invalidMedia is a file whose bytes do not hold what its name claims.
type invalidMedia string

func (e invalidMedia) Error() string { return string(e) }

// Media validation reads the bytes, not just the file name.

func pngSize(data []byte) (width, height int, err error) {
	if len(data) < 24 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) || !bytes.Equal(data[12:16], []byte("IHDR")) {
		return 0, 0, invalidMedia("not a PNG")
	}
	return int(binary.BigEndian.Uint32(data[16:20])), int(binary.BigEndian.Uint32(data[20:24])), nil
}

func jpegSize(data []byte) (width, height int, err error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, 0, invalidMedia("not a JPEG")
	}
	for i := 2; i+9 < len(data); {
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		switch marker {
		case 0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF:
			return int(binary.BigEndian.Uint16(data[i+7 : i+9])), int(binary.BigEndian.Uint16(data[i+5 : i+7])), nil
		}
		i += 2 + int(binary.BigEndian.Uint16(data[i+2:i+4]))
	}
	return 0, 0, invalidMedia("JPEG has no frame header")
}

// frame is one recorded screencast frame and the time it was captured, in milliseconds from the start of the recording.
type frame struct {
	path string
	ms   float64
}

func le16(values ...int) []byte {
	out := make([]byte, 0, 2*len(values))
	for _, v := range values {
		out = binary.LittleEndian.AppendUint16(out, uint16(v))
	}
	return out
}

func le32(values ...int) []byte {
	out := make([]byte, 0, 4*len(values))
	for _, v := range values {
		out = binary.LittleEndian.AppendUint32(out, uint32(v))
	}
	return out
}

func join(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

// writeMJPEGAVI writes an AVI container holding the original JPEG frames (MJPEG). Each source frame is held until the next frame's
// timestamp at a constant frame rate; the held repeats are container timing, the pixels are the originals. It returns the number of video
// frames and how many of them are held repeats.
func writeMJPEGAVI(frames []frame, out string) (total, held int, err error) {
	if len(frames) == 0 {
		return 0, 0, invalidMedia("no frames")
	}
	const fps = videoFPS
	var payloads [][]byte
	for index, f := range frames {
		data, err := os.ReadFile(f.path)
		if err != nil {
			return 0, 0, err
		}
		nextMS := f.ms + 1000/fps
		if index+1 < len(frames) {
			nextMS = frames[index+1].ms
		}
		repeats := max(1, int(math.RoundToEven((nextMS-f.ms)*fps/1000)))
		for range repeats {
			payloads = append(payloads, data)
		}
	}
	width, height, err := jpegSize(payloads[0]) // From the bytes, not the recorder's metadata.
	if err != nil {
		return 0, 0, err
	}
	total = len(payloads)
	movi := join([]byte("LIST"), le32(0), []byte("movi"))
	var index []byte
	for _, data := range payloads {
		offset := len(movi) - 8
		movi = join(movi, []byte("00dc"), le32(len(data)), data)
		if len(data)%2 == 1 {
			movi = append(movi, 0)
		}
		index = join(index, []byte("00dc"), le32(0x10, offset, len(data)))
	}
	binary.LittleEndian.PutUint32(movi[4:], uint32(len(movi)-8))
	avih := le32(1000000/fps, 0, 0, 0x10, total, 0, 1, 0, width, height, 0, 0, 0, 0)
	strh := join([]byte("vidsMJPG"), le32(0), le16(0, 0), le32(0, 1, fps, 0, total, 0, 0, 0), le16(0, 0, width, height))
	strf := join(le32(40, width, height), le16(1, 24), []byte("MJPG"), le32(width*height*3, 0, 0, 0, 0)) // BITMAPINFOHEADER
	strl := join([]byte("LIST"), le32(4+8+len(strh)+8+len(strf)), []byte("strlstrh"), le32(len(strh)), strh, []byte("strf"), le32(len(strf)), strf)
	hdrlBody := join([]byte("hdrlavih"), le32(len(avih)), avih, strl)
	hdrl := join([]byte("LIST"), le32(len(hdrlBody)), hdrlBody)
	idx1 := join([]byte("idx1"), le32(len(index)), index)
	body := join([]byte("AVI "), hdrl, movi, idx1)
	if err := os.WriteFile(out, join([]byte("RIFF"), le32(len(body)), body), 0o644); err != nil {
		return 0, 0, err
	}
	return total, total - len(frames), nil
}

// inspectAVI adds what the container itself says to info, after checking that every video chunk it declares is present and is a JPEG.
func inspectAVI(data []byte, info *value.Object) error {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "AVI " {
		return invalidMedia("not an AVI")
	}
	pos := bytes.Index(data, []byte("avih"))
	if pos < 0 {
		return invalidMedia("AVI has no avih header")
	}
	if pos+64 > len(data) {
		return invalidMedia("AVI avih header is truncated")
	}
	field := func(n int) int { return int(binary.LittleEndian.Uint32(data[pos+8+4*n:])) }
	microPerFrame, totalFrames, width, height := field(0), field(4), field(8), field(9)
	chunks := 0
	i := bytes.Index(data, []byte("movi")) + 4
	end := bytes.Index(data, []byte("idx1"))
	if end <= 0 {
		end = len(data)
	}
	for i+8 <= end {
		size := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		if string(data[i:i+4]) == "00dc" {
			chunks++
			if i+10 > len(data) || data[i+8] != 0xFF || data[i+9] != 0xD8 {
				return invalidMedia(fmt.Sprintf("frame %d is not a JPEG", chunks))
			}
		}
		i += 8 + size + size%2
	}
	if chunks != totalFrames {
		return invalidMedia(fmt.Sprintf("avih says %d frames, container holds %d", totalFrames, chunks))
	}
	info.Set("type", "video/x-msvideo")
	info.Set("codec", "MJPG")
	info.Set("width", width)
	info.Set("height", height)
	info.Set("frames", chunks)
	if microPerFrame == 0 {
		info.Set("fps", 0)
		info.Set("duration_ms", 0)
		return nil
	}
	info.Set("fps", round3(1_000_000/float64(microPerFrame)))
	info.Set("duration_ms", int(math.RoundToEven(float64(chunks)*float64(microPerFrame)/1000)))
	return nil
}

// round3 rounds to three decimal places from the number's exact value, not from a product that has already lost precision.
func round3(f float64) value.Float {
	rounded, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'f', 3, 64), 64)
	return value.Float(rounded)
}

// suffix is the file name's last extension in lower case; a name that only starts with a dot has none.
func suffix(name string) string {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[dot:])
}

func inspectMP4(path string, info *value.Object) error {
	name := filepath.Base(path)
	probe, err := proc.Run([]string{"ffprobe", "-v", "error", "-select_streams", "v:0", "-count_frames", "-show_entries",
		"stream=width,height,nb_read_frames:format=duration", "-of", "json", path}, proc.Options{})
	if err != nil {
		return invalidMedia(fmt.Sprintf("ffprobe could not read %s: %v", name, err))
	}
	unreadable := invalidMedia(fmt.Sprintf("ffprobe could not read %s: %s", name, head(strings.TrimSpace(probe.Stderr), 200)))
	parsed, err := value.Decode([]byte(probe.Stdout))
	if err != nil {
		return unreadable
	}
	streams := value.List(value.Obj(parsed).Get("streams"))
	if len(streams) == 0 {
		return unreadable
	}
	stream := value.Obj(streams[0])
	width, okW := value.Number(stream.Get("width"))
	height, okH := value.Number(stream.Get("height"))
	count, errCount := strconv.Atoi(value.Str(stream.Get("nb_read_frames")))
	duration, errDuration := strconv.ParseFloat(value.Str(value.Obj(value.Obj(parsed).Get("format")).Get("duration")), 64)
	if !okW || !okH || errCount != nil || errDuration != nil {
		return unreadable
	}
	info.Set("type", "video/mp4")
	info.Set("width", int(width))
	info.Set("height", int(height))
	info.Set("frames", count)
	info.Set("duration_ms", int(math.RoundToEven(duration*1000)))
	return nil
}

// InspectMedia reads a media file and reports what its bytes hold: size, hash, type, and for images and video the real dimensions,
// frame count, and duration. An empty file, image, or video is an error.
func InspectMedia(path string) (*value.Object, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	info := value.NewObject("file", name, "bytes", len(data), "sha256", tool.SHA256(data))
	ext := suffix(name)
	image := func(mime string, size func([]byte) (int, int, error)) error {
		width, height, err := size(data)
		if err != nil {
			return err
		}
		info.Set("type", mime)
		info.Set("width", width)
		info.Set("height", height)
		return nil
	}
	switch {
	case ext == ".png":
		err = image("image/png", pngSize)
	case ext == ".jpg" || ext == ".jpeg":
		err = image("image/jpeg", jpegSize)
	case ext == ".avi":
		err = inspectAVI(data, info)
	case ext == ".mp4" && onPath("ffprobe"):
		err = inspectMP4(path, info)
	case ext == ".txt" || ext == ".json" || ext == ".log" || ext == ".html":
		info.Set("type", "text/plain")
	default:
		info.Set("type", "application/octet-stream")
	}
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"width", "height", "frames", "bytes"} {
		if n, ok := value.Number(info.Get(key)); ok && n == 0 {
			return nil, invalidMedia(fmt.Sprintf("%s is empty (%s)", name, value.Repr(info)))
		}
	}
	return info, nil
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
