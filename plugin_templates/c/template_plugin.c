#define _POSIX_C_SOURCE 200809L

#include <ctype.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <time.h>
#include <unistd.h>

#define PLUGIN_NAME "template_c"

static const char *plugin_name(void) {
    const char *name = getenv("TARRAGON_PLUGIN_NAME");
    return name && *name ? name : PLUGIN_NAME;
}

static void log_msg(const char *msg) {
    fprintf(stderr, "[PLUGIN: %s] %s\n", plugin_name(), msg);
}

static char *copy_string(const char *s) {
    size_t len = strlen(s);
    char *out = malloc(len + 1);
    if (!out) return NULL;
    memcpy(out, s, len + 1);
    return out;
}

static char *join_args(int argc, char **argv, int start) {
    size_t len = 0;
    for (int i = start; i < argc; i++) {
        len += strlen(argv[i]) + (i > start ? 1 : 0);
    }
    char *out = calloc(len + 1, 1);
    if (!out) return NULL;
    for (int i = start; i < argc; i++) {
        if (i > start) strcat(out, " ");
        strcat(out, argv[i]);
    }
    return out;
}

static char *json_escape(const char *s) {
    size_t len = 0;
    for (const char *p = s; *p; p++) {
        len += (*p == '\\' || *p == '"' || *p == '\n' || *p == '\r' || *p == '\t') ? 2 : 1;
    }
    char *out = malloc(len + 1);
    if (!out) return NULL;
    char *w = out;
    for (const char *p = s; *p; p++) {
        switch (*p) {
        case '\\': *w++ = '\\'; *w++ = '\\'; break;
        case '"': *w++ = '\\'; *w++ = '"'; break;
        case '\n': *w++ = '\\'; *w++ = 'n'; break;
        case '\r': *w++ = '\\'; *w++ = 'r'; break;
        case '\t': *w++ = '\\'; *w++ = 't'; break;
        default: *w++ = *p; break;
        }
    }
    *w = '\0';
    return out;
}

static char *reverse_copy(const char *s) {
    size_t len = strlen(s);
    char *out = malloc(len + 1);
    if (!out) return NULL;
    for (size_t i = 0; i < len; i++) out[i] = s[len - 1 - i];
    out[len] = '\0';
    return out;
}

static char *upper_copy(const char *s) {
    size_t len = strlen(s);
    char *out = malloc(len + 1);
    if (!out) return NULL;
    for (size_t i = 0; i < len; i++) out[i] = (char)toupper((unsigned char)s[i]);
    out[len] = '\0';
    return out;
}

static char *bracket_copy(const char *s) {
    size_t len = strlen(s);
    char *out = malloc(len + 3);
    if (!out) return NULL;
    snprintf(out, len + 3, "[%s]", s);
    return out;
}

static char *payload(const char *text) {
    char *escaped_text = json_escape(text);
    char *variants[3] = {reverse_copy(text), upper_copy(text), bracket_copy(text)};
    char *escaped[3] = {NULL, NULL, NULL};
    if (!escaped_text || !variants[0] || !variants[1] || !variants[2]) goto fail;
    for (int i = 0; i < 3; i++) {
        escaped[i] = json_escape(variants[i]);
        if (!escaped[i]) goto fail;
    }

    const char *fmt = "{\"input\":\"%s\",\"variants\":["
                      "{\"id\":\"1\",\"label\":\"%s\",\"actions\":[{\"name\":\"select\",\"default\":true,\"description\":\"Acknowledge selection\"}]},"
                      "{\"id\":\"2\",\"label\":\"%s\",\"actions\":[{\"name\":\"select\",\"default\":true,\"description\":\"Acknowledge selection\"}]},"
                      "{\"id\":\"3\",\"label\":\"%s\",\"actions\":[{\"name\":\"select\",\"default\":true,\"description\":\"Acknowledge selection\"}]}]}";
    int needed = snprintf(NULL, 0, fmt, escaped_text, escaped[0], escaped[1], escaped[2]);
    if (needed < 0) goto fail;
    char *out = malloc((size_t)needed + 1);
    if (!out) goto fail;
    snprintf(out, (size_t)needed + 1, fmt, escaped_text, escaped[0], escaped[1], escaped[2]);

    free(escaped_text);
    for (int i = 0; i < 3; i++) {
        free(variants[i]);
        free(escaped[i]);
    }
    return out;

fail:
    free(escaped_text);
    for (int i = 0; i < 3; i++) {
        free(variants[i]);
        free(escaped[i]);
    }
    return NULL;
}

static char *json_value(const char *json, const char *key) {
    char marker[128];
    snprintf(marker, sizeof(marker), "\"%s\"", key);
    const char *p = strstr(json, marker);
    if (!p) return copy_string("");
    p = strchr(p + strlen(marker), ':');
    if (!p) return copy_string("");
    p = strchr(p, '"');
    if (!p) return copy_string("");
    p++;
    char *out = calloc(strlen(p) + 1, 1);
    if (!out) return NULL;
    char *w = out;
    int escaped = 0;
    for (; *p; p++) {
        if (escaped) {
            *w++ = *p == 'n' ? '\n' : *p == 'r' ? '\r' : *p == 't' ? '\t' : *p;
            escaped = 0;
            continue;
        }
        if (*p == '\\') {
            escaped = 1;
            continue;
        }
        if (*p == '"') break;
        *w++ = *p;
    }
    *w = '\0';
    return out;
}

static int connect_unix(const char *endpoint) {
    int fd = -1;
    for (int attempt = 0; attempt < 20; attempt++) {
        fd = socket(AF_UNIX, SOCK_STREAM, 0);
        if (fd < 0) return -1;
        struct sockaddr_un addr;
        memset(&addr, 0, sizeof(addr));
        addr.sun_family = AF_UNIX;
        snprintf(addr.sun_path, sizeof(addr.sun_path), "%s", endpoint);
        if (connect(fd, (struct sockaddr *)&addr, sizeof(addr)) == 0) return fd;
        close(fd);
        struct timespec delay = {.tv_sec = 0, .tv_nsec = 100000000};
        nanosleep(&delay, NULL);
    }
    return -1;
}

static int write_all(int fd, const char *s) {
    size_t left = strlen(s);
    while (left > 0) {
        ssize_t n = write(fd, s, left);
        if (n < 0) return -1;
        s += n;
        left -= (size_t)n;
    }
    return 0;
}

static int read_line(int fd, char *buf, size_t size) {
    size_t pos = 0;
    while (pos + 1 < size) {
        char ch;
        ssize_t n = read(fd, &ch, 1);
        if (n == 0) break;
        if (n < 0) return -1;
        if (ch == '\n') break;
        buf[pos++] = ch;
    }
    buf[pos] = '\0';
    return pos > 0 ? 1 : 0;
}

static int run_daemon(const char *endpoint) {
    int fd = connect_unix(endpoint);
    if (fd < 0) return 1;

    char hello[256];
    snprintf(hello, sizeof(hello), "{\"type\":\"hello\",\"name\":\"%s\"}\n", plugin_name());
    if (write_all(fd, hello) != 0) return 1;
    log_msg("connected");

    char line[65536];
    while (read_line(fd, line, sizeof(line)) > 0) {
        char *type = json_value(line, "type");
        char *qid = json_value(line, "query_id");
        if (!type || !qid) return 1;
        if (strcmp(type, "request") == 0) {
            char *text = json_value(line, "text");
            char *data = payload(text ? text : "");
            char *escaped_qid = json_escape(qid);
            if (!text || !data || !escaped_qid) return 1;
            size_t len = strlen(data) + strlen(escaped_qid) + 64;
            char *resp = malloc(len);
            if (!resp) return 1;
            snprintf(resp, len, "{\"type\":\"response\",\"query_id\":\"%s\",\"data\":%s}\n", escaped_qid, data);
            write_all(fd, resp);
            free(text); free(data); free(escaped_qid); free(resp);
        } else if (strcmp(type, "select") == 0) {
            write_all(fd, "{\"type\":\"select_response\",\"success\":true,\"message\":\"selected\"}\n");
        }
        free(type);
        free(qid);
    }
    close(fd);
    return 0;
}

int main(int argc, char **argv) {
    if (argc >= 4 && strcmp(argv[1], "tarragon") == 0 && strcmp(argv[2], "query") == 0) {
        char *text = join_args(argc, argv, 3);
        char *data = payload(text ? text : "");
        if (!data) return 1;
        printf("%s\n", data);
        free(text);
        free(data);
        return 0;
    }

    const char *endpoint = getenv("TARRAGON_PLUGINS_ENDPOINT");
    if (!endpoint || !*endpoint) {
        log_msg("idle mode; TARRAGON_PLUGINS_ENDPOINT is not set");
        pause();
        return 0;
    }
    return run_daemon(endpoint);
}
