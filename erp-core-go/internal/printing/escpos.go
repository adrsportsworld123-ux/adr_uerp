// Package printing implements the ESC/POS command set (see
// phase0_1_design.md §3.9 and pos_frd_complete.md §12) — the one printer
// protocol Phase 1's roadmap scoped this to ("pick whichever brand you'll
// actually pilot with — Zebra or a generic ESC/POS printer"), chosen here
// because a single ESC/POS-speaking thermal printer commonly serves both
// the receipt printer AND the label printer role in a small retail
// register, satisfying the FRD's "one printer integration" with one
// driver rather than two.
//
// This package only ever produces raw bytes; nothing in this repo talks
// to a physical or virtual printer port. Deliberately so — there is no
// printer hardware or emulator in this build environment, exactly the
// same constraint that limited offline sync (erp-pos-flutter) to
// sqflite_common_ffi rather than an on-device test. Correctness here is
// verified the equivalent way: Decode (decode.go) parses the generated
// byte stream back into structured commands so tests can assert the
// actual command bytes a real printer would receive and act on are
// correct, rather than asserting nothing beyond "it compiles." On-device
// verification against real hardware remains open — see the roadmap.
package printing

import "bytes"

// ESC/POS command bytes, kept as raw escape sequences rather than named
// per-use functions for every combination — Builder's methods below are
// the actual API; these are just the wire format each one emits.
const (
	cmdInit        = "\x1B\x40"     // ESC @  — reset to defaults
	cmdAlignLeft   = "\x1B\x61\x00" // ESC a 0
	cmdAlignCenter = "\x1B\x61\x01" // ESC a 1
	cmdBoldOn      = "\x1B\x45\x01" // ESC E 1
	cmdBoldOff     = "\x1B\x45\x00" // ESC E 0
	cmdDoubleOn    = "\x1D\x21\x11" // GS !  0x11 — double height + width
	cmdDoubleOff   = "\x1D\x21\x00" // GS !  0x00 — normal size
	cmdCutFull     = "\x1D\x56\x00" // GS V 0 — full cut
	cmdBarcodeHt   = "\x1D\x68"     // GS h <n>  — barcode height in dots
	cmdBarcodeWid  = "\x1D\x77"     // GS w <n>  — barcode module width
	cmdBarcodeHRI  = "\x1D\x48"     // GS H <n>  — human-readable text position
	cmdBarcodePrnB = "\x1D\x6B"     // GS k <m> <n> <data> — Function B (explicit length, no NUL terminator)
)

// Barcode symbology codes for the GS k "Function B" form — the byte
// values are fixed by the ESC/POS spec, not something this package
// invents. Only the two this codebase's barcodes.symbology values
// actually use (see migrations/001_schema.sql's CHECK constraint) are
// wired up as constructors below (BarcodeEAN13/BarcodeCode128); the rest
// are listed for completeness since a printer that receives an
// unsupported value would silently misprint rather than error.
const (
	symUPCA    byte = 65
	symUPCE    byte = 66
	symEAN13   byte = 67
	symEAN8    byte = 68
	symCODE39  byte = 69
	symITF     byte = 70
	symCODABAR byte = 71
	symCODE93  byte = 72
	symCODE128 byte = 73
)

// HRI (human-readable interpretation) position values for GS H.
const (
	HRINone  byte = 0
	HRIAbove byte = 1
	HRIBelow byte = 2
	HRIBoth  byte = 3
)

// Builder assembles one print job's worth of ESC/POS commands. Every
// method returns the receiver so calls can be chained; there is no
// implicit flush — call Bytes() once the job is fully built.
type Builder struct {
	buf bytes.Buffer
}

func New() *Builder {
	b := &Builder{}
	b.buf.WriteString(cmdInit)
	return b
}

func (b *Builder) Text(s string) *Builder {
	b.buf.WriteString(s)
	return b
}

func (b *Builder) Line(s string) *Builder {
	b.buf.WriteString(s)
	b.buf.WriteByte('\n')
	return b
}

func (b *Builder) FeedLines(n int) *Builder {
	for i := 0; i < n; i++ {
		b.buf.WriteByte('\n')
	}
	return b
}

func (b *Builder) AlignLeft() *Builder   { b.buf.WriteString(cmdAlignLeft); return b }
func (b *Builder) AlignCenter() *Builder { b.buf.WriteString(cmdAlignCenter); return b }

func (b *Builder) Bold(on bool) *Builder {
	if on {
		b.buf.WriteString(cmdBoldOn)
	} else {
		b.buf.WriteString(cmdBoldOff)
	}
	return b
}

func (b *Builder) DoubleSize(on bool) *Builder {
	if on {
		b.buf.WriteString(cmdDoubleOn)
	} else {
		b.buf.WriteString(cmdDoubleOff)
	}
	return b
}

// Barcode emits height/width/HRI-position setup commands immediately
// followed by the GS k print command itself — the ESC/POS spec requires
// those setup commands to precede the print command in the stream, so
// this bundles them into one call rather than leaving callers to
// remember the ordering.
func (b *Builder) Barcode(symbology byte, height, width, hriPosition byte, data string) *Builder {
	b.buf.WriteString(cmdBarcodeHt)
	b.buf.WriteByte(height)
	b.buf.WriteString(cmdBarcodeWid)
	b.buf.WriteByte(width)
	b.buf.WriteString(cmdBarcodeHRI)
	b.buf.WriteByte(hriPosition)
	b.buf.WriteString(cmdBarcodePrnB)
	b.buf.WriteByte(symbology)
	b.buf.WriteByte(byte(len(data)))
	b.buf.WriteString(data)
	return b
}

// BarcodeEAN13 prints a 12- or 13-digit EAN-13 code (the printer computes
// and appends the check digit itself if given 12) at the given height/
// width with the human-readable digits shown below the bars — the layout
// pos_frd_complete.md §12's label templates call for.
func (b *Builder) BarcodeEAN13(code string, height, width byte) *Builder {
	return b.Barcode(symEAN13, height, width, HRIBelow, code)
}

func (b *Builder) BarcodeCode128(code string, height, width byte) *Builder {
	return b.Barcode(symCODE128, height, width, HRIBelow, code)
}

func (b *Builder) Cut() *Builder {
	b.buf.WriteString(cmdCutFull)
	return b
}

func (b *Builder) Bytes() []byte {
	return b.buf.Bytes()
}
