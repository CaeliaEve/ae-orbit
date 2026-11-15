import java.io.BufferedWriter;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.ResultSetMetaData;
import java.sql.SQLException;
import java.sql.Statement;
import java.util.Locale;

/**
 * Streams an NESQL HSQLDB export as UTF-8 NDJSON without mutating the source.
 * The reader deliberately emits strings for every database value so that the
 * Python normalizer can preserve NBT and localized text byte-for-byte enough
 * for catalog auditing.
 */
public final class NesqlDumpReader {
    private NesqlDumpReader() {}

    public static void main(String[] args) throws Exception {
        if (args.length < 2 || args.length > 3) {
            System.err.println("usage: NesqlDumpReader <source-dir> <output.ndjson> [per-table-limit]");
            System.exit(2);
        }
        Path source = Paths.get(args[0]).toAbsolutePath().normalize();
        Path output = Paths.get(args[1]).toAbsolutePath().normalize();
        int limit = args.length == 3 ? Integer.parseInt(args[2]) : 0;
        if (!Files.isDirectory(source)) {
            throw new IOException("source is not a directory: " + source);
        }
        Path database = source.resolve("nesql-db");
        String url = "jdbc:hsqldb:file:" + database + ";readonly=true;shutdown=false";
        Files.createDirectories(output.getParent());
        try (Connection connection = DriverManager.getConnection(url, "SA", "");
             BufferedWriter writer = Files.newBufferedWriter(output, StandardCharsets.UTF_8)) {
            connection.setReadOnly(true);
            writeTable(connection, writer, "ITEM", limit);
            writeTable(connection, writer, "ITEM_TOOLTIP", limit);
        }
    }

    private static void writeTable(Connection connection, BufferedWriter writer, String table, int limit)
            throws SQLException, IOException {
        String sql = "SELECT * FROM PUBLIC." + table;
        if (limit > 0) {
            sql += " LIMIT " + limit;
        }
        try (Statement statement = connection.createStatement();
             ResultSet result = statement.executeQuery(sql)) {
            ResultSetMetaData metadata = result.getMetaData();
            int columns = metadata.getColumnCount();
            StringBuilder schema = new StringBuilder(table).append(':');
            for (int i = 1; i <= columns; i++) {
                if (i > 1) schema.append(',');
                schema.append(metadata.getColumnLabel(i));
            }
            System.err.println(schema);
            while (result.next()) {
                writer.write("{\"table\":\"");
                writeJsonString(writer, table.toLowerCase(Locale.ROOT));
                writer.write("\",\"row\":{");
                for (int i = 1; i <= columns; i++) {
                    if (i > 1) writer.write(',');
                    writer.write('"');
                    writeJsonString(writer, metadata.getColumnLabel(i).toLowerCase(Locale.ROOT));
                    writer.write("\":");
                    String value = result.getString(i);
                    if (value == null) {
                        writer.write("null");
                    } else {
                        writer.write('"');
                        writeJsonString(writer, value);
                        writer.write('"');
                    }
                }
                writer.write("}}\n");
            }
        }
    }

    private static void writeJsonString(BufferedWriter writer, String value) throws IOException {
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            switch (c) {
                case '"': writer.write("\\\""); break;
                case '\\': writer.write("\\\\"); break;
                case '\b': writer.write("\\b"); break;
                case '\f': writer.write("\\f"); break;
                case '\n': writer.write("\\n"); break;
                case '\r': writer.write("\\r"); break;
                case '\t': writer.write("\\t"); break;
                default:
                    if (c < 0x20) {
                        writer.write(String.format(Locale.ROOT, "\\u%04x", (int) c));
                    } else {
                        writer.write(c);
                    }
            }
        }
    }
}
