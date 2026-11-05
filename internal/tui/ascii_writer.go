package tui

import "io"

type asciiFallbackWriter struct {
	Writer io.Writer
	ASCII  bool
}

func (writer asciiFallbackWriter) Write(contents []byte) (int, error) {
	if !writer.ASCII {
		return writer.Writer.Write(contents)
	}
	converted := []byte(asciiText(string(contents)))
	written, err := writer.Writer.Write(converted)
	if err != nil {
		return 0, err
	}
	if written != len(converted) {
		return 0, io.ErrShortWrite
	}
	return len(contents), nil
}
