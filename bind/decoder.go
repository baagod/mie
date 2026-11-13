package bind

import (
    "encoding/json/v2"
    "errors"
    "net/http"
    "reflect"
    "strings"

    "github.com/baagod/mie/internal/xsync"
)

const (
    MIMEAppJSON           = "application/json"
    MIMEAppFormUrlencoded = "application/x-www-form-urlencoded"
)

type Decoder interface {
    Decode(b []byte, in any) error
}

var decoders xsync.Map[string, Decoder]

type jsonDecoder struct{}

func init() {
    Register(MIMEAppJSON, jsonDecoder{})
}

func Register(ct string, d Decoder) {
    if _, loaded := decoders.LoadOrStore(strings.ToLower(ct), d); loaded {
        panic("bind: duplicate decoder for " + ct)
    }
}

func Lookup(ct string) (Decoder, bool) {
    return decoders.Load(strings.ToLower(ct))
}

func (jsonDecoder) Decode(b []byte, in any) error {
    return json.Unmarshal(b, in)
}

// Decode 把请求里的位置来源写进 in；in 必须是 *IN，body 由调用方处理。
func Decode[IN any](r *http.Request, in *IN) (err error) {
    if in == nil {
        return errors.New("bind: destination must be a non-nil pointer to IN")
    }

    t := reflect.TypeFor[IN]()
    ptr := t.Kind() == reflect.Pointer
    if ptr {
        t = t.Elem()
    }

    p := planFor(t)
    if p.err != nil {
        return p.err
    }

    v := reflect.ValueOf(in).Elem() // IN

    // IN 是 *T：请求期分配 T，handler 永远拿到非 nil 指针。
    if ptr {
        if v.IsNil() {
            v.Set(reflect.New(t))
        }
        v = v.Elem()
    }

    // 只有存在参数来源时才 ParseForm，避免多读/多解析 body。
    if p.hasForm {
        if err = r.ParseForm(); err != nil {
            return errors.New("bind params: " + err.Error())
        }
    }

    for _, f := range p.fields {
        if err = bindValue(r, v.Field(f.index), f); err != nil {
            return
        }
    }

    return
}
