package languages

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

const sampleMyBatisMapper = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE mapper PUBLIC "-//mybatis.org//DTD Mapper 3.0//EN"
        "http://mybatis.org/dtd/mybatis-3-mapper.dtd">
<mapper namespace="com.app.UserMapper">
    <select id="findUser" resultType="com.app.User">
        SELECT id, name FROM users WHERE id = #{id}
    </select>
    <insert id="insertUser">
        INSERT INTO users (name) VALUES (#{name})
    </insert>
    <update id="updateUser">
        UPDATE users SET name = #{name}
        <where>
            <if test="id != null">id = #{id}</if>
        </where>
    </update>
    <delete id="deleteUser">DELETE FROM users WHERE id = #{id}</delete>
    <sql id="cols">id, name</sql>
    <resultMap id="userMap" type="com.app.User"/>
</mapper>`

func TestMyBatisExtractor_StatementNodes(t *testing.T) {
	res, err := NewMyBatisExtractor().Extract("UserMapper.xml", []byte(sampleMyBatisMapper))
	require.NoError(t, err)

	var file *graph.Node
	stmts := map[string]*graph.Node{}
	for _, n := range res.Nodes {
		switch n.Kind {
		case graph.KindFile:
			file = n
		case graph.KindMethod:
			stmts[n.ID] = n
		}
	}
	require.NotNil(t, file)
	require.Equal(t, "com.app.UserMapper", file.Meta["mybatis_namespace"])

	// One node per <select>/<insert>/<update>/<delete> — <sql> and
	// <resultMap> are excluded.
	require.Len(t, stmts, 4)

	cases := []struct {
		id      string
		kind    string
		sqlPart string
	}{
		{"com.app.UserMapper::findUser", "select", "SELECT id, name FROM users"},
		{"com.app.UserMapper::insertUser", "insert", "INSERT INTO users"},
		{"com.app.UserMapper::updateUser", "update", "UPDATE users SET name"},
		{"com.app.UserMapper::deleteUser", "delete", "DELETE FROM users"},
	}
	for _, c := range cases {
		n := stmts[c.id]
		require.NotNil(t, n, "missing statement node %s", c.id)
		require.Equal(t, c.kind, n.Meta["mybatis_sql_kind"])
		require.Equal(t, "com.app.UserMapper", n.Meta["mybatis_namespace"])
		sql, _ := n.Meta["mybatis_sql"].(string)
		require.Contains(t, sql, c.sqlPart, "statement %s SQL", c.id)
	}

	// The dynamic-SQL <where>/<if> body is flattened into the stored SQL.
	require.Contains(t, stmts["com.app.UserMapper::updateUser"].Meta["mybatis_sql"], "id = #{id}")

	// Each statement emits a placeholder call edge keyed by namespace::id.
	placeholders := map[string]bool{}
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls {
			if via, _ := e.Meta["via"].(string); via == "mybatis.mapper" {
				placeholders[e.To] = true
			}
		}
	}
	require.True(t, placeholders["unresolved::mybatis::com.app.UserMapper::findUser"])
	require.Len(t, placeholders, 4)
}

func TestMyBatisExtractor_NonMapperXMLYieldsOnlyFileNode(t *testing.T) {
	plain := []byte(`<?xml version="1.0"?><config><setting name="x">1</setting></config>`)
	res, err := NewMyBatisExtractor().Extract("config.xml", plain)
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1) // file node only
	require.Equal(t, graph.KindFile, res.Nodes[0].Kind)
	require.Empty(t, res.Edges)
}

func TestIsMyBatisMapper(t *testing.T) {
	require.True(t, IsMyBatisMapper([]byte(`<mapper namespace="com.app.X">`)))
	require.True(t, IsMyBatisMapper([]byte(`<!DOCTYPE mapper PUBLIC "-//mybatis.org//DTD Mapper 3.0//EN" "...">`)))
	require.False(t, IsMyBatisMapper([]byte(`<config><x/></config>`)))
	require.False(t, IsMyBatisMapper([]byte(`<mapper>no namespace here</mapper>`)))
}

func TestMyBatisExtractor_Malformed(t *testing.T) {
	res, err := NewMyBatisExtractor().Extract("bad.xml", []byte(`<mapper namespace="com.app.X"><select id="q">SELECT 1`))
	require.NoError(t, err)        // never a hard failure
	require.NotEmpty(t, res.Nodes) // at least the file node survives
}

func TestMyBatisExtractor_Extensions(t *testing.T) {
	require.Equal(t, []string{".xml"}, NewMyBatisExtractor().Extensions())
}

func TestMyBatisExtractor_SignatureString(t *testing.T) {
	src := []byte(`<?xml version="1.0"?>
<!DOCTYPE mapper PUBLIC "-//mybatis.org//DTD Mapper 3.0//EN" "x">
<mapper namespace="com.app.UserMapper">
  <sql id="cols">id, name</sql>
  <select id="findUser" parameterType="Long" resultType="User">SELECT * FROM users</select>
  <select id="byMap" resultMap="userMap">SELECT *</select>
</mapper>`)
	res, err := NewMyBatisExtractor().Extract("UserMapper.xml", src)
	require.NoError(t, err)

	sigs := map[string]string{}
	for _, n := range res.Nodes {
		if n.Meta != nil {
			if s, ok := n.Meta["signature"].(string); ok {
				sigs[n.Name] = s
			}
		}
	}
	require.Equal(t, "SELECT param=Long result=User", sigs["findUser"])
	require.Equal(t, "SELECT result=userMap", sigs["byMap"]) // resultMap fallback
	require.Equal(t, "<sql>", sigs["cols"])

	// The structured mybatis_* keys remain intact alongside the signature.
	for _, n := range res.Nodes {
		if n.Name == "findUser" {
			require.Equal(t, "select", n.Meta["mybatis_sql_kind"])
			require.Equal(t, "Long", n.Meta["mybatis_parameter_type"])
			require.Equal(t, "User", n.Meta["mybatis_result_type"])
		}
	}
}

func TestMyBatisExtractor_LineNumbersMultiStatement(t *testing.T) {
	// Line numbers come from the precomputed line-start table + binary search;
	// assert they match the source positions exactly across several elements.
	src := []byte("<?xml version=\"1.0\"?>\n" + // line 1
		"<!DOCTYPE mapper PUBLIC \"-//mybatis.org//DTD Mapper 3.0//EN\" \"x\">\n" + // line 2
		"<mapper namespace=\"com.app.M\">\n" + // line 3
		"  <sql id=\"cols\">id, name</sql>\n" + // line 4
		"  <select id=\"a\">SELECT 1</select>\n" + // line 5
		"  <insert id=\"b\">INSERT</insert>\n" + // line 6
		"</mapper>\n")
	res, err := NewMyBatisExtractor().Extract("M.xml", src)
	require.NoError(t, err)

	lines := map[string]int{}
	for _, n := range res.Nodes {
		if n.Kind == graph.KindMethod || n.Kind == graph.KindFunction {
			lines[n.Name] = n.StartLine
		}
	}
	require.Equal(t, 4, lines["cols"])
	require.Equal(t, 5, lines["a"])
	require.Equal(t, 6, lines["b"])
}

// BenchmarkMyBatisLineResolution contrasts resolving every element offset to a
// line via the precomputed line-start table + binary search (one O(n) scan,
// then O(log n) per offset) against the previous per-element full-prefix
// newline count (O(offset) per element → O(n·m) overall). The "precomputed"
// sub-benchmark is asymptotically faster and allocation-free per offset.
func BenchmarkMyBatisLineResolution(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&sb, "  <select id=\"q%d\">SELECT 1</select>\n", i)
	}
	src := []byte(sb.String())
	var offs []int
	for i, c := range src {
		if c == '\n' {
			offs = append(offs, i)
		}
	}

	b.Run("precomputed_binary_search", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			starts := lineStartOffsets(src)
			sum := 0
			for _, off := range offs {
				sum += lineForOffset(starts, off)
			}
			_ = sum
		}
	})
	b.Run("per_element_prefix_count", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			sum := 0
			for _, off := range offs {
				sum += 1 + bytes.Count(src[:off], []byte{'\n'})
			}
			_ = sum
		}
	})
}
