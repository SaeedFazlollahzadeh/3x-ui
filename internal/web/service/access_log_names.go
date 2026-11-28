package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type accessLogNames struct {
	clients  map[string]string
	inbounds map[string]string
}

type accessLogLabels struct {
	ClientName  string `json:"client_name,omitempty"`
	InboundName string `json:"inbound_name,omitempty"`
}

const accessLogNameMarker = "\t# xui: "

func readAccessLogNames(ctx context.Context) accessLogNames {
	db := database.GetDB()
	if db == nil {
		return accessLogNames{}
	}
	names := accessLogNames{clients: make(map[string]string), inbounds: make(map[string]string)}
	var clients []struct{ Email, Comment string }
	if err := db.WithContext(ctx).Model(&model.ClientRecord{}).Select("email, comment").
		Where("comment <> ?", "").Find(&clients).Error; err == nil {
		for _, row := range clients {
			names.clients[strings.ToLower(row.Email)] = strings.TrimSpace(row.Comment)
		}
	}
	var inbounds []struct{ Tag, Remark string }
	if err := db.WithContext(ctx).Model(&model.Inbound{}).Select("tag, remark").
		Where("node_id IS NULL AND remark <> ?", "").Find(&inbounds).Error; err == nil {
		for _, row := range inbounds {
			names.inbounds[strings.ToLower(row.Tag)] = strings.TrimSpace(row.Remark)
		}
	}
	return names
}

func (names accessLogNames) labels(entry LogEntry, recorded accessLogLabels) accessLogLabels {
	if recorded.ClientName == "" {
		recorded.ClientName = names.clients[strings.ToLower(entry.Email)]
	}
	if recorded.InboundName == "" {
		recorded.InboundName = names.inbounds[strings.ToLower(entry.Inbound)]
	}
	return recorded
}

func splitAccessLogNames(line string) (string, accessLogLabels) {
	i := strings.LastIndex(line, accessLogNameMarker)
	var labels accessLogLabels
	if i >= 0 && json.Unmarshal([]byte(line[i+len(accessLogNameMarker):]), &labels) == nil {
		return line[:i], labels
	}
	return line, accessLogLabels{}
}

func isInternalAccessLogLine(line []byte) bool {
	return bytes.Contains(line, []byte("[api -> api]"))
}

// annotateAccessLog snapshots display names on completed log lines so archived
// events remain readable after a client or inbound is renamed or deleted.
func annotateAccessLog(ctx context.Context, path string, names accessLogNames) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return writeActivityBackup(path, func(output io.Writer) error {
		reader := bufio.NewReaderSize(file, maxAccessLogLineBytes)
		continuation, internal := false, false
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunk, err := reader.ReadSlice('\n')
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
				return err
			}
			if !continuation {
				internal = isInternalAccessLogLine(chunk)
			}
			if !internal && !continuation && err == nil {
				line := strings.TrimSuffix(strings.TrimSuffix(string(chunk), "\n"), "\r")
				labels := names.labels(parseAccessLogFields(line), accessLogLabels{})
				if labels != (accessLogLabels{}) {
					data, err := json.Marshal(labels)
					if err != nil {
						return err
					}
					ending := string(chunk[len(line):])
					chunk = []byte(line + accessLogNameMarker + string(data) + ending)
				}
			}
			if !internal {
				if _, err := output.Write(chunk); err != nil {
					return err
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			continuation = errors.Is(err, bufio.ErrBufferFull)
		}
	})
}
