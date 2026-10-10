package repository

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/hyaeve/manco/internal/model"
)

var gzipMagic = []byte{0x1f, 0x8b}

// decodeRepositoryPayload accepts the three repository formats used by
// Kototoro, Mihon and legacy extension indexes: plain JSON, JSON arrays and
// gzip-compressed protobuf (index.pb).
func decodeRepositoryPayload(raw []byte, base *url.URL, repoKind string) ([]model.RepositoryExtension, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("仓库清单为空")
	}
	if bytes.HasPrefix(raw, gzipMagic) {
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("无法解压仓库清单：%w", err)
		}
		defer reader.Close()
		decoded, err := gzipReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("无法读取解压后的仓库清单：%w", err)
		}
		raw = decoded
	}
	if len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') {
		var payload any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("仓库清单不是可识别的 JSON：%w", err)
		}
		return compact(parsePayload(payload, base, NormalizeKind(repoKind))), nil
	}
	items, err := decodeProtobufIndex(raw, base, NormalizeKind(repoKind))
	if err != nil {
		return nil, err
	}
	return compact(items), nil
}

func gzipReadAll(reader *gzip.Reader) ([]byte, error) {
	var output bytes.Buffer
	_, err := output.ReadFrom(reader)
	return output.Bytes(), err
}

// decodeProtobufIndex implements the shared Mihon/Kototoro index.proto
// contract without pulling an Android/JVM runtime into the server.
func decodeProtobufIndex(raw []byte, base *url.URL, pluginType string) ([]model.RepositoryExtension, error) {
	rootFields, err := parseProtoFields(raw)
	if err != nil {
		return nil, fmt.Errorf("仓库清单不是可识别的 protobuf：%w", err)
	}
	var listFields []protoField
	for _, field := range rootFields {
		if field.number == 101 && field.wire == 2 {
			listFields, err = parseProtoFields(field.bytes)
			if err != nil {
				return nil, fmt.Errorf("无法解析扩展列表：%w", err)
			}
			break
		}
	}
	if len(listFields) == 0 {
		return nil, fmt.Errorf("仓库清单中没有扩展列表")
	}
	items := make([]model.RepositoryExtension, 0, len(listFields))
	for _, field := range listFields {
		if field.number != 1 || field.wire != 2 {
			continue
		}
		extension, err := parseProtoExtension(field.bytes, base, pluginType)
		if err != nil {
			continue
		}
		items = append(items, extension)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("仓库清单中没有可识别的扩展")
	}
	return items, nil
}

func parseProtoExtension(raw []byte, base *url.URL, pluginType string) (model.RepositoryExtension, error) {
	fields, err := parseProtoFields(raw)
	if err != nil {
		return model.RepositoryExtension{}, err
	}
	var item model.RepositoryExtension
	item.PluginType = NormalizeKind(pluginType)
	for _, field := range fields {
		switch field.number {
		case 1:
			item.Name = string(field.bytes)
		case 2:
			item.PackageName = string(field.bytes)
		case 3:
			resources, _ := parseProtoFields(field.bytes)
			for _, resource := range resources {
				switch resource.number {
				case 1:
					item.InstallURL = absolute(base, string(resource.bytes))
				case 2:
					item.Icon = absolute(base, string(resource.bytes))
				case 501:
					jarURL := absolute(base, string(resource.bytes))
					if jarURL != "" {
						item.InstallURL = jarURL
					}
				}
			}
		case 4:
			item.Description = "扩展运行环境：" + string(field.bytes)
		case 5:
			item.Version = strconv.FormatUint(field.varint, 10)
		case 6:
			item.Version = string(field.bytes)
		case 7:
			// ContentWarning is metadata only; it does not change source kind.
		case 8:
			source, sourceErr := parseProtoSource(field.bytes, base)
			if sourceErr == nil && source.Name != "" {
				item.Sources = append(item.Sources, source)
			}
		}
	}
	if item.Name == "" {
		item.Name = item.PackageName
	}
	if item.Name == "" {
		return model.RepositoryExtension{}, fmt.Errorf("扩展名称为空")
	}
	if len(item.Sources) > 0 {
		item.Homepage = item.Sources[0].HomeURL
	}
	item.Kind = classifySource(item)
	item.ID = stableID(item.PackageName + "|" + item.Version)
	item.Raw = marshalRaw(map[string]any{
		"name":        item.Name,
		"packageName": item.PackageName,
		"version":     item.Version,
		"sources":     item.Sources,
	})
	return item, nil
}

func parseProtoSource(raw []byte, base *url.URL) (model.RepositorySource, error) {
	fields, err := parseProtoFields(raw)
	if err != nil {
		return model.RepositorySource{}, err
	}
	var item model.RepositorySource
	for _, field := range fields {
		switch field.number {
		case 1:
			item.ID = strconv.FormatUint(field.varint, 10)
		case 2:
			item.Name = string(field.bytes)
		case 3:
			item.Language = string(field.bytes)
		case 4:
			item.HomeURL = absolute(base, string(field.bytes))
		case 5:
			item.Mirrors = append(item.Mirrors, absolute(base, string(field.bytes)))
		case 7:
			item.Message = string(field.bytes)
		}
	}
	return item, nil
}

type protoField struct {
	number int
	wire   int
	varint uint64
	bytes  []byte
}

func parseProtoFields(raw []byte) ([]protoField, error) {
	fields := make([]protoField, 0, 8)
	for len(raw) > 0 {
		key, count := readUvarint(raw)
		if count == 0 {
			return nil, fmt.Errorf("无效的 protobuf 字段头")
		}
		raw = raw[count:]
		field := protoField{number: int(key >> 3), wire: int(key & 7)}
		if field.number <= 0 {
			return nil, fmt.Errorf("无效的 protobuf 字段编号")
		}
		switch field.wire {
		case 0:
			value, length := readUvarint(raw)
			if length == 0 {
				return nil, fmt.Errorf("字段 %d 的 varint 不完整", field.number)
			}
			field.varint = value
			raw = raw[length:]
		case 1:
			if len(raw) < 8 {
				return nil, fmt.Errorf("字段 %d 的 fixed64 不完整", field.number)
			}
			field.bytes = append([]byte(nil), raw[:8]...)
			raw = raw[8:]
		case 2:
			length, lengthBytes := readUvarint(raw)
			if lengthBytes == 0 || length > uint64(len(raw)-lengthBytes) {
				return nil, fmt.Errorf("字段 %d 的长度不完整", field.number)
			}
			raw = raw[lengthBytes:]
			field.bytes = append([]byte(nil), raw[:int(length)]...)
			raw = raw[int(length):]
		case 5:
			if len(raw) < 4 {
				return nil, fmt.Errorf("字段 %d 的 fixed32 不完整", field.number)
			}
			field.bytes = append([]byte(nil), raw[:4]...)
			raw = raw[4:]
		default:
			return nil, fmt.Errorf("字段 %d 使用了不支持的 wire type %d", field.number, field.wire)
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func readUvarint(raw []byte) (uint64, int) {
	var value uint64
	for index, current := range raw {
		if index >= 10 {
			return 0, 0
		}
		value |= uint64(current&0x7f) << (7 * index)
		if current < 0x80 {
			return value, index + 1
		}
	}
	return 0, 0
}

func classifySource(item model.RepositoryExtension) string {
	text := strings.ToLower(strings.Join(append([]string{item.Name, item.PackageName, item.Description}, sourceNames(item.Sources)...), " "))
	return classifyKindText(text)
}

func sourceNames(sources []model.RepositorySource) []string {
	names := make([]string, 0, len(sources))
	for _, item := range sources {
		names = append(names, item.Name)
	}
	return names
}
