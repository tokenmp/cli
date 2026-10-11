package configedit

import (
	"os"
	"strings"
	"testing"
)

// fieldConfig 是字段改写的起点：两条渠道，带条目注释、字段行内注释与一段嵌套映射。
const fieldConfig = `# 服务配置
channels:
  # 主渠道
  - name: example
    dialect: openai
    base_url: https://api.example.com/v1 # 基址
    auth:
      # 凭据变量
      style: bearer
      key_env: EXAMPLE_KEY

  # 备用渠道
  - name: backup
    dialect: claude
    base_url: https://api.backup.com
    auth:
      style: none
`

func TestSetSequenceFieldChangesExistingScalar(t *testing.T) {
	path := sandbox(t, fieldConfig)

	err := SetSequenceField(path, []string{"channels"}, 0, []string{"base_url"}, "https://api.changed.com", nil)
	if err != nil {
		t.Fatalf("SetSequenceField: %v", err)
	}

	got := readBack(t, path)
	if !strings.Contains(got, "base_url: https://api.changed.com") {
		t.Errorf("base_url 没有改掉:\n%s", got)
	}
	if strings.Contains(got, "https://api.example.com/v1") {
		t.Errorf("旧值还在:\n%s", got)
	}
	// 条目注释与字段行内注释都要保留。
	for _, want := range []string{"# 主渠道", "# 备用渠道", "# 凭据变量", "# 基址"} {
		if !strings.Contains(got, want) {
			t.Errorf("注释 %q 没有保留:\n%s", want, got)
		}
	}
}

func TestSetSequenceFieldMultiSegment(t *testing.T) {
	path := sandbox(t, fieldConfig)

	if err := SetSequenceField(path, []string{"channels"}, 0, []string{"auth", "key_env"}, "CHANGED_KEY", nil); err != nil {
		t.Fatalf("SetSequenceField: %v", err)
	}

	got := readBack(t, path)
	if !strings.Contains(got, "key_env: CHANGED_KEY") {
		t.Errorf("嵌套字段没有改掉:\n%s", got)
	}
	if strings.Contains(got, "EXAMPLE_KEY") {
		t.Errorf("旧值还在:\n%s", got)
	}
	if !strings.Contains(got, "style: bearer") {
		t.Errorf("同级的字段被动了:\n%s", got)
	}
	// 两格缩进保留。
	if !strings.Contains(got, "    auth:\n") || !strings.Contains(got, "      key_env: CHANGED_KEY") {
		t.Errorf("嵌套缩进不对:\n%s", got)
	}
}

func TestSetSequenceFieldAddsMissingLeaf(t *testing.T) {
	path := sandbox(t, fieldConfig)

	if err := SetSequenceField(path, []string{"channels"}, 1, []string{"auth", "key_env"}, "BACKUP_KEY", nil); err != nil {
		t.Fatalf("SetSequenceField: %v", err)
	}

	got := readBack(t, path)
	if !strings.Contains(got, "style: none") || !strings.Contains(got, "key_env: BACKUP_KEY") {
		t.Errorf("缺的叶子字段应当补出来:\n%s", got)
	}
}

func TestSetSequenceFieldRemovesOnNil(t *testing.T) {
	path := sandbox(t, fieldConfig)

	if err := SetSequenceField(path, []string{"channels"}, 0, []string{"auth", "key_env"}, nil, nil); err != nil {
		t.Fatalf("SetSequenceField: %v", err)
	}

	got := readBack(t, path)
	if strings.Contains(got, "key_env") {
		t.Errorf("nil 应当删掉字段:\n%s", got)
	}
	if !strings.Contains(got, "style: bearer") {
		t.Errorf("删字段不该动同级字段:\n%s", got)
	}
}

func TestSetSequenceFieldIndexOutOfRange(t *testing.T) {
	path := sandbox(t, fieldConfig)
	before := readBack(t, path)

	err := SetSequenceField(path, []string{"channels"}, 5, []string{"base_url"}, "https://x", nil)
	if err == nil {
		t.Fatal("越界应当报错")
	}
	for _, want := range []string{"路径 channels", "下标 5", "越界"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息 %q 里没有 %q", err.Error(), want)
		}
	}
	if readBack(t, path) != before {
		t.Error("越界时不该改动文件")
	}

	// 负数同样按越界处理。
	if err := SetSequenceField(path, []string{"channels"}, -1, []string{"base_url"}, "https://x", nil); err == nil {
		t.Fatal("负数下标应当报错")
	}
}

func TestSetSequenceFieldMissingField(t *testing.T) {
	path := sandbox(t, fieldConfig)
	before := readBack(t, path)

	err := SetSequenceField(path, []string{"channels"}, 0, []string{"missing", "key_env"}, "X", nil)
	if err == nil {
		t.Fatal("中间段不存在应当报错")
	}
	for _, want := range []string{"路径 channels", "下标 0", "字段 missing 不存在"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息 %q 里没有 %q", err.Error(), want)
		}
	}
	if readBack(t, path) != before {
		t.Error("字段不存在时不该改动文件")
	}
}

func TestSetSequenceFieldNonMappingMiddle(t *testing.T) {
	path := sandbox(t, fieldConfig)

	err := SetSequenceField(path, []string{"channels"}, 0, []string{"name", "inner"}, "X", nil)
	if err == nil {
		t.Fatal("中间段不是映射应当报错")
	}
	if !strings.Contains(err.Error(), "字段 name 不是映射") {
		t.Errorf("错误信息应当点出不是映射: %v", err)
	}
}

func TestSetSequenceFieldPathErrors(t *testing.T) {
	path := sandbox(t, fieldConfig)

	if err := SetSequenceField(path, nil, 0, []string{"name"}, "X", nil); err == nil {
		t.Error("空路径应当报错")
	}
	if err := SetSequenceField(path, []string{"channels"}, 0, nil, "X", nil); err == nil {
		t.Error("空字段路径应当报错")
	}
	err := SetSequenceField(path, []string{"plugins", "list"}, 0, []string{"key"}, "X", nil)
	if err == nil || !strings.Contains(err.Error(), "路径 plugins 不存在") {
		t.Errorf("序列不存在应当报错并点出路径: %v", err)
	}
}

func TestSetSequenceFieldCheckFailureRestores(t *testing.T) {
	path := sandbox(t, fieldConfig)
	before := readBack(t, path)

	err := SetSequenceField(path, []string{"channels"}, 0, []string{"base_url"}, "https://api.changed.com", func() error {
		return os.ErrInvalid
	})
	if err == nil {
		t.Fatal("校验失败应当报错")
	}
	if !strings.Contains(err.Error(), "已还原") {
		t.Errorf("错误信息应当说明已还原: %v", err)
	}
	if readBack(t, path) != before {
		t.Errorf("校验失败后文件没有还原\n--- got ---\n%s\n--- want ---\n%s", readBack(t, path), before)
	}
}

func TestSetSequenceFieldsIsAtomic(t *testing.T) {
	path := sandbox(t, fieldConfig)
	before := readBack(t, path)

	// 第一项合法，第二项的中间段不存在：整个改写都应当失败，文件不动。
	err := SetSequenceFields(path, []string{"channels"}, 0, []SequenceField{
		{Path: []string{"base_url"}, Value: "https://api.changed.com"},
		{Path: []string{"missing", "key_env"}, Value: "X"},
	}, nil)
	if err == nil {
		t.Fatal("有一项失败时整批应当失败")
	}
	if readBack(t, path) != before {
		t.Errorf("整批失败时不该写一半\n--- got ---\n%s", readBack(t, path))
	}
}

func TestSetSequenceFieldsWritesAll(t *testing.T) {
	path := sandbox(t, fieldConfig)

	err := SetSequenceFields(path, []string{"channels"}, 0, []SequenceField{
		{Path: []string{"dialect"}, Value: "claude"},
		{Path: []string{"auth", "key_env"}, Value: "CHANGED_KEY"},
		{Path: []string{"auth", "style"}, Value: "x-api-key"},
	}, nil)
	if err != nil {
		t.Fatalf("SetSequenceFields: %v", err)
	}

	got := readBack(t, path)
	for _, want := range []string{"dialect: claude", "key_env: CHANGED_KEY", "style: x-api-key"} {
		if !strings.Contains(got, want) {
			t.Errorf("结果里没有 %q:\n%s", want, got)
		}
	}
}
