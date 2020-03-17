// Copyright 2013 Joshua Tacoma. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package uritemplates is a level 4 implementation of RFC 6570 (URI
// Template, http://tools.ietf.org/html/rfc6570).
//
// To use uritemplates, parse a template string and expand it with a value
// map:
//
//	template, _ := uritemplates.Parse("https://api.github.com/repos{/user,repo}")
//	values := make(map[string]interface{})
//	values["user"] = "jtacoma"
//	values["repo"] = "uritemplates"
//	expanded, _ := template.Expand(values)
//	fmt.Printf(expanded)
//
package uritemplates

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var (
	unreserved = regexp.MustCompile("[^A-Za-z0-9\\-._~]")
	reserved   = regexp.MustCompile("[^A-Za-z0-9\\-._~:/?#[\\]@!$&'()*+,;=]")
	validname  = regexp.MustCompile("^([A-Za-z0-9_\\.]|%[0-9A-Fa-f][0-9A-Fa-f])+$")
	hex        = []byte("0123456789ABCDEF")
)

func pctEncode(src []byte) []byte {
	dst := make([]byte, len(src)*3)
	for i, b := range src {
		buf := dst[i*3 : i*3+3]
		buf[0] = 0x25
		buf[1] = hex[b/16]
		buf[2] = hex[b%16]
	}
	return dst
}

func escape(s string, allowReserved bool) (escaped string) {
	if allowReserved {
		escaped = string(reserved.ReplaceAllFunc([]byte(s), pctEncode))
	} else {
		escaped = string(unreserved.ReplaceAllFunc([]byte(s), pctEncode))
	}
	return escaped
}

// A UriTemplate is a parsed representation of a URI template.
type UriTemplate struct {
	Parts []TemplatePart
	Raw   string
}

// Parse parses a URI template string into a UriTemplate object.
func Parse(rawtemplate string) (template *UriTemplate, err error) {
	template = new(UriTemplate)
	template.Raw = rawtemplate
	split := strings.Split(rawtemplate, "{")
	template.Parts = make([]TemplatePart, len(split)*2-1)
	for i, s := range split {
		if i == 0 {
			if strings.Contains(s, "}") {
				err = errors.New("unexpected }")
				break
			}
			template.Parts[i].Raw = s
		} else {
			subsplit := strings.Split(s, "}")
			if len(subsplit) != 2 {
				err = errors.New("malformed template")
				break
			}
			expression := subsplit[0]
			template.Parts[i*2-1], err = parseExpression(expression)
			if err != nil {
				break
			}
			template.Parts[i*2].Raw = subsplit[1]
		}
	}
	if err != nil {
		template = nil
	}
	return template, err
}

func (t UriTemplate) String() string {
	return t.Raw
}

type TemplatePart struct {
	Terms         []TemplateTerm
	Raw           string
	First         string
	Sep           string
	Named         bool
	Ifemp         string
	AllowReserved bool
}

type TemplateTerm struct {
	Name     string
	Explode  bool
	Truncate int
}

func parseExpression(expression string) (result TemplatePart, err error) {
	switch expression[0] {
	case '+':
		result.Sep = ","
		result.AllowReserved = true
		expression = expression[1:]
	case '.':
		result.First = "."
		result.Sep = "."
		expression = expression[1:]
	case '/':
		result.First = "/"
		result.Sep = "/"
		expression = expression[1:]
	case ';':
		result.First = ";"
		result.Sep = ";"
		result.Named = true
		expression = expression[1:]
	case '?':
		result.First = "?"
		result.Sep = "&"
		result.Named = true
		result.Ifemp = "="
		expression = expression[1:]
	case '&':
		result.First = "&"
		result.Sep = "&"
		result.Named = true
		result.Ifemp = "="
		expression = expression[1:]
	case '#':
		result.First = "#"
		result.Sep = ","
		result.AllowReserved = true
		expression = expression[1:]
	default:
		result.Sep = ","
	}
	rawterms := strings.Split(expression, ",")
	result.Terms = make([]TemplateTerm, len(rawterms))
	for i, raw := range rawterms {
		result.Terms[i], err = parseTerm(raw)
		if err != nil {
			break
		}
	}
	return result, err
}

func parseTerm(term string) (result TemplateTerm, err error) {
	if strings.HasSuffix(term, "*") {
		result.Explode = true
		term = term[:len(term)-1]
	}
	split := strings.Split(term, ":")
	if len(split) == 1 {
		result.Name = term
	} else if len(split) == 2 {
		result.Name = split[0]
		var parsed int64
		parsed, err = strconv.ParseInt(split[1], 10, 0)
		result.Truncate = int(parsed)
	} else {
		err = errors.New("multiple colons in same term")
	}
	if !validname.MatchString(result.Name) {
		err = errors.New("not a valid name: " + result.Name)
	}
	if result.Explode && result.Truncate > 0 {
		err = errors.New("both explode and prefix modifers on same term")
	}
	return result, err
}

// Names returns the names of all variables within the template.
func (self *UriTemplate) Names() []string {
	names := make([]string, 0, len(self.Parts))

	for _, p := range self.Parts {
		if len(p.Raw) > 0 || len(p.Terms) == 0 {
			continue
		}

		for _, term := range p.Terms {
			names = append(names, term.Name)
		}
	}

	return names
}

// Expand expands a URI template with a set of values to produce a string.
func (self *UriTemplate) Expand(value interface{}) (string, error) {
	values, ismap := value.(map[string]interface{})
	if !ismap {
		if m, ismap := struct2map(value); !ismap {
			return "", errors.New("expected map[string]interface{}, struct, or pointer to struct.")
		} else {
			return self.Expand(m)
		}
	}
	var buf bytes.Buffer
	for _, p := range self.Parts {
		err := p.expand(&buf, values)
		if err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

func (self *TemplatePart) expand(buf *bytes.Buffer, values map[string]interface{}) error {
	if len(self.Raw) > 0 {
		buf.WriteString(self.Raw)
		return nil
	}
	var zeroLen = buf.Len()
	buf.WriteString(self.First)
	var firstLen = buf.Len()
	for _, term := range self.Terms {
		value, exists := values[term.Name]
		// do not add to the template if the value is nil
		if !exists || value == nil {
			continue
		}

		// if the value is an empty string, change the position
		// so the modifier is kept:  "X{.empty}" => "X.", rather than just "X",
		if value == "" {
			zeroLen = firstLen
		}

		if buf.Len() != firstLen {
			buf.WriteString(self.Sep)
		}
		switch v := value.(type) {
		case string:
			self.expandString(buf, term, v)
		case []interface{}:
			self.expandArray(buf, term, v)
		case map[string]interface{}:
			if term.Truncate > 0 {
				return errors.New("cannot truncate a map expansion")
			}
			self.expandMap(buf, term, v)
		default:
			if m, ismap := struct2map(value); ismap {
				if term.Truncate > 0 {
					return errors.New("cannot truncate a map expansion")
				}
				self.expandMap(buf, term, m)
			} else {
				str := fmt.Sprintf("%v", value)
				self.expandString(buf, term, str)
			}
		}
	}
	if buf.Len() == firstLen {
		original := buf.Bytes()[:zeroLen]
		buf.Reset()
		buf.Write(original)
	}
	return nil
}

func (self *TemplatePart) expandName(buf *bytes.Buffer, name string, empty bool) {
	if self.Named {
		buf.WriteString(name)
		if empty {
			buf.WriteString(self.Ifemp)
		} else {
			buf.WriteString("=")
		}
	}
}

func (self *TemplatePart) expandString(buf *bytes.Buffer, t TemplateTerm, s string) {
	if len(s) > t.Truncate && t.Truncate > 0 {
		s = s[:t.Truncate]
	}
	self.expandName(buf, t.Name, len(s) == 0)
	buf.WriteString(escape(s, self.AllowReserved))
}

func (self *TemplatePart) expandArray(buf *bytes.Buffer, t TemplateTerm, a []interface{}) {
	if len(a) == 0 {
		return
	} else if !t.Explode {
		self.expandName(buf, t.Name, false)
	}
	for i, value := range a {
		if t.Explode && i > 0 {
			buf.WriteString(self.Sep)
		} else if i > 0 {
			buf.WriteString(",")
		}
		var s string
		switch v := value.(type) {
		case string:
			s = v
		default:
			s = fmt.Sprintf("%v", v)
		}
		if len(s) > t.Truncate && t.Truncate > 0 {
			s = s[:t.Truncate]
		}
		if self.Named && t.Explode {
			self.expandName(buf, t.Name, len(s) == 0)
		}
		buf.WriteString(escape(s, self.AllowReserved))
	}
}

func (self *TemplatePart) expandMap(buf *bytes.Buffer, t TemplateTerm, m map[string]interface{}) {
	if len(m) == 0 {
		return
	}
	if !t.Explode {
		self.expandName(buf, t.Name, len(m) == 0)
	}
	var firstLen = buf.Len()
	for k, value := range m {
		if firstLen != buf.Len() {
			if t.Explode {
				buf.WriteString(self.Sep)
			} else {
				buf.WriteString(",")
			}
		}
		var s string
		switch v := value.(type) {
		case string:
			s = v
		default:
			s = fmt.Sprintf("%v", v)
		}
		if t.Explode {
			buf.WriteString(escape(k, self.AllowReserved))
			buf.WriteRune('=')
			buf.WriteString(escape(s, self.AllowReserved))
		} else {
			buf.WriteString(escape(k, self.AllowReserved))
			buf.WriteRune(',')
			buf.WriteString(escape(s, self.AllowReserved))
		}
	}
}

func struct2map(v interface{}) (map[string]interface{}, bool) {
	value := reflect.ValueOf(v)

	switch value.Type().Kind() {
	case reflect.Ptr:
		return struct2map(value.Elem().Interface())
	case reflect.Struct:
		m := make(map[string]interface{})
		for i := 0; i < value.NumField(); i++ {
			tag := value.Type().Field(i).Tag
			var name string
			if strings.Contains(string(tag), ":") {
				name = tag.Get("uri")
			} else {
				name = strings.TrimSpace(string(tag))
			}
			if len(name) == 0 {
				name = value.Type().Field(i).Name
			}
			m[name] = value.Field(i).Interface()
		}
		return m, true
	}
	return nil, false
}
