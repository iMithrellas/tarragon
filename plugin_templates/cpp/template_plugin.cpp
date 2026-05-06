#include <algorithm>
#include <cerrno>
#include <chrono>
#include <cctype>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>
#include <sys/socket.h>
#include <sys/un.h>
#include <thread>
#include <unistd.h>
#include <vector>

namespace {

std::string plugin_name() {
    const char* name = std::getenv("TARRAGON_PLUGIN_NAME");
    return name && *name ? name : "template_cpp";
}

void log(const std::string& msg) {
    std::cerr << "[PLUGIN: " << plugin_name() << "] " << msg << "\n";
}

std::string json_escape(const std::string& value) {
    std::ostringstream out;
    for (char ch : value) {
        switch (ch) {
        case '\\': out << "\\\\"; break;
        case '"': out << "\\\""; break;
        case '\n': out << "\\n"; break;
        case '\r': out << "\\r"; break;
        case '\t': out << "\\t"; break;
        default: out << ch; break;
        }
    }
    return out.str();
}

std::string quote(const std::string& value) {
    return "\"" + json_escape(value) + "\"";
}

std::string json_value(const std::string& json, const std::string& key) {
    const std::string marker = "\"" + key + "\"";
    size_t pos = json.find(marker);
    if (pos == std::string::npos) return "";
    pos = json.find(':', pos + marker.size());
    if (pos == std::string::npos) return "";
    pos = json.find('"', pos + 1);
    if (pos == std::string::npos) return "";
    std::string out;
    bool escaped = false;
    for (size_t i = pos + 1; i < json.size(); ++i) {
        char ch = json[i];
        if (escaped) {
            switch (ch) {
            case 'n': out.push_back('\n'); break;
            case 'r': out.push_back('\r'); break;
            case 't': out.push_back('\t'); break;
            default: out.push_back(ch); break;
            }
            escaped = false;
            continue;
        }
        if (ch == '\\') {
            escaped = true;
            continue;
        }
        if (ch == '"') break;
        out.push_back(ch);
    }
    return out;
}

std::vector<std::string> variants(const std::string& text) {
    std::string reversed = text;
    std::reverse(reversed.begin(), reversed.end());
    std::string upper = text;
    std::transform(upper.begin(), upper.end(), upper.begin(), [](unsigned char c) { return std::toupper(c); });
    std::string bracketed = "[" + text + "]";
    return {reversed, upper, bracketed};
}

std::string payload(const std::string& text) {
    auto vals = variants(text);
    std::ostringstream out;
    out << "{\"input\":" << quote(text) << ",\"variants\":[";
    for (size_t i = 0; i < vals.size(); ++i) {
        if (i) out << ',';
        out << "{\"id\":" << quote(std::to_string(i + 1))
            << ",\"label\":" << quote(vals[i])
            << ",\"actions\":[{\"name\":\"select\",\"default\":true,\"description\":\"Acknowledge selection\"}]}";
    }
    out << "]}";
    return out.str();
}

void write_all(int fd, const std::string& data) {
    const char* ptr = data.data();
    size_t left = data.size();
    while (left > 0) {
        ssize_t written = ::write(fd, ptr, left);
        if (written < 0) throw std::runtime_error(std::strerror(errno));
        ptr += written;
        left -= static_cast<size_t>(written);
    }
}

bool read_line(int fd, std::string& line) {
    line.clear();
    char ch = 0;
    while (true) {
        ssize_t n = ::read(fd, &ch, 1);
        if (n == 0) return !line.empty();
        if (n < 0) throw std::runtime_error(std::strerror(errno));
        if (ch == '\n') return true;
        line.push_back(ch);
    }
}

int connect_unix(const std::string& endpoint) {
    int fd = -1;
    std::string last;
    for (int attempt = 0; attempt < 20; ++attempt) {
        fd = ::socket(AF_UNIX, SOCK_STREAM, 0);
        if (fd < 0) throw std::runtime_error(std::strerror(errno));
        sockaddr_un addr{};
        addr.sun_family = AF_UNIX;
        std::snprintf(addr.sun_path, sizeof(addr.sun_path), "%s", endpoint.c_str());
        if (::connect(fd, reinterpret_cast<sockaddr*>(&addr), sizeof(addr)) == 0) return fd;
        last = std::strerror(errno);
        ::close(fd);
        std::this_thread::sleep_for(std::chrono::milliseconds(100));
    }
    throw std::runtime_error(last);
}

void run_daemon(const std::string& endpoint) {
    int fd = connect_unix(endpoint);
    write_all(fd, "{\"type\":\"hello\",\"name\":" + quote(plugin_name()) + "}\n");
    log("connected to " + endpoint);

    std::string line;
    while (read_line(fd, line)) {
        std::string type = json_value(line, "type");
        std::string qid = json_value(line, "query_id");
        if (type == "request") {
            std::string text = json_value(line, "text");
            write_all(fd, "{\"type\":\"response\",\"query_id\":" + quote(qid) + ",\"data\":" + payload(text) + "}\n");
        } else if (type == "select") {
            std::string result_id = json_value(line, "result_id");
            write_all(fd, "{\"type\":\"select_response\",\"success\":true,\"message\":" + quote("selected " + result_id) + "}\n");
        }
    }
    ::close(fd);
}

} // namespace

int main(int argc, char** argv) {
    if (argc >= 4 && std::string(argv[1]) == "tarragon" && std::string(argv[2]) == "query") {
        std::ostringstream text;
        for (int i = 3; i < argc; ++i) {
            if (i > 3) text << ' ';
            text << argv[i];
        }
        std::cout << payload(text.str()) << "\n";
        return 0;
    }

    const char* endpoint = std::getenv("TARRAGON_PLUGINS_ENDPOINT");
    if (!endpoint || !*endpoint) {
        log("idle mode; TARRAGON_PLUGINS_ENDPOINT is not set");
        pause();
        return 0;
    }

    try {
        run_daemon(endpoint);
        return 0;
    } catch (const std::exception& e) {
        log(std::string("error: ") + e.what());
        return 1;
    }
}
