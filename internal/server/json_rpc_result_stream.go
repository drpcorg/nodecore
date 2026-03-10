package server

import (
	"encoding/json"
	"fmt"
	"io"
)

func streamJsonRPCResult(reader io.Reader, output io.Writer) error {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()

	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("unable to parse stream response: %w", err)
	}
	objectStart, ok := token.(json.Delim)
	if !ok || objectStart != '{' {
		return fmt.Errorf("unable to parse stream response: expected json object")
	}

	resultFound := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("unable to parse stream response: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("unable to parse stream response: invalid json key token %T", keyToken)
		}

		valueToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("unable to parse stream response: %w", err)
		}
		if key == "result" {
			resultFound = true
			if err := writeJSONValue(output, decoder, valueToken); err != nil {
				return err
			}
		} else {
			if err := skipJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
	}

	objectEnd, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("unable to parse stream response: %w", err)
	}
	endDelim, ok := objectEnd.(json.Delim)
	if !ok || endDelim != '}' {
		return fmt.Errorf("unable to parse stream response: expected json object end")
	}
	if !resultFound {
		return fmt.Errorf("unable to parse stream response: result field is missing")
	}
	return nil
}

func skipJSONValue(decoder *json.Decoder, token json.Token) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("unable to parse stream response: %w", err)
			}
			if _, ok := keyToken.(string); !ok {
				return fmt.Errorf("unable to parse stream response: invalid json key token %T", keyToken)
			}
			valueToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("unable to parse stream response: %w", err)
			}
			if err := skipJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("unable to parse stream response: %w", err)
		}
		endDelim, ok := end.(json.Delim)
		if !ok || endDelim != '}' {
			return fmt.Errorf("unable to parse stream response: expected json object end")
		}
	case '[':
		for decoder.More() {
			valueToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("unable to parse stream response: %w", err)
			}
			if err := skipJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("unable to parse stream response: %w", err)
		}
		endDelim, ok := end.(json.Delim)
		if !ok || endDelim != ']' {
			return fmt.Errorf("unable to parse stream response: expected json array end")
		}
	default:
		return fmt.Errorf("unable to parse stream response: unexpected json delimiter %q", delim)
	}

	return nil
}

func writeJSONValue(output io.Writer, decoder *json.Decoder, token json.Token) error {
	delim, ok := token.(json.Delim)
	if ok {
		switch delim {
		case '{':
			if _, err := output.Write([]byte{'{'}); err != nil {
				return err
			}
			first := true
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return fmt.Errorf("unable to parse stream response: %w", err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("unable to parse stream response: invalid json key token %T", keyToken)
				}
				if !first {
					if _, err := output.Write([]byte{','}); err != nil {
						return err
					}
				}
				keyBytes, err := json.Marshal(key)
				if err != nil {
					return err
				}
				if _, err := output.Write(keyBytes); err != nil {
					return err
				}
				if _, err := output.Write([]byte{':'}); err != nil {
					return err
				}

				valueToken, err := decoder.Token()
				if err != nil {
					return fmt.Errorf("unable to parse stream response: %w", err)
				}
				if err := writeJSONValue(output, decoder, valueToken); err != nil {
					return err
				}
				first = false
			}

			end, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("unable to parse stream response: %w", err)
			}
			endDelim, ok := end.(json.Delim)
			if !ok || endDelim != '}' {
				return fmt.Errorf("unable to parse stream response: expected json object end")
			}
			_, err = output.Write([]byte{'}'})
			return err
		case '[':
			if _, err := output.Write([]byte{'['}); err != nil {
				return err
			}
			first := true
			for decoder.More() {
				if !first {
					if _, err := output.Write([]byte{','}); err != nil {
						return err
					}
				}
				valueToken, err := decoder.Token()
				if err != nil {
					return fmt.Errorf("unable to parse stream response: %w", err)
				}
				if err := writeJSONValue(output, decoder, valueToken); err != nil {
					return err
				}
				first = false
			}

			end, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("unable to parse stream response: %w", err)
			}
			endDelim, ok := end.(json.Delim)
			if !ok || endDelim != ']' {
				return fmt.Errorf("unable to parse stream response: expected json array end")
			}
			_, err = output.Write([]byte{']'})
			return err
		default:
			return fmt.Errorf("unable to parse stream response: unexpected json delimiter %q", delim)
		}
	}

	primitive, err := jsonTokenBytes(token)
	if err != nil {
		return err
	}
	_, err = output.Write(primitive)
	return err
}

func jsonTokenBytes(token json.Token) ([]byte, error) {
	switch value := token.(type) {
	case nil:
		return []byte("null"), nil
	case bool:
		if value {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case string:
		return json.Marshal(value)
	case json.Number:
		return []byte(value.String()), nil
	default:
		return json.Marshal(value)
	}
}
