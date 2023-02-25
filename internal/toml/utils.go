package toml

import (
	"errors"
	"fmt"
	"os"

	tomllib "github.com/pelletier/go-toml/v2"
)

func DecodeFile(fpath string, v interface{}) error {
	fs, err := os.Open(fpath)
	if err != nil {
		return err
	}
	defer fs.Close()

	dec := tomllib.NewDecoder(fs)
	err = dec.Decode(v)
	if err != nil {
		var derr *tomllib.DecodeError
		if errors.As(err, &derr) {
			row, col := derr.Position()
			return fmt.Errorf("%s\ntoml error occurred at line %d column %d", derr.String(), row, col)
		}
		return err
	}
	return nil
}
