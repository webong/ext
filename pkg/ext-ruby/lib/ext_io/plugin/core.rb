# frozen_string_literal: true

require "strscan"

module ExtIO
  module Plugin
    # What the guest and frames code share: the wire limits, the patterns, strict JSON decoding
    # and encoding, and the RFC 3339 deadline.

    API_VERSION = "ext.plugin/v1"
    MAX_FRAME_BYTES = 24 << 20
    MAX_RETRY_AFTER_MS = 365 * 24 * 60 * 60 * 1000
    MAX_MESSAGE_BYTES = 4096
    MAX_DEPTH = 64

    IDENTIFIER = /\A[a-z0-9][a-z0-9._\/-]{0,255}\z/.freeze
    REVISION = /\A[A-Za-z0-9][A-Za-z0-9._:+\/-]{0,255}\z/.freeze
    TIME = /\A(\d{4})-(\d\d)-(\d\d)[Tt](\d\d):(\d\d):(\d\d)(\.\d+)?([Zz]|[+-]\d\d:\d\d)\z/.freeze

    # The peer violated the framing or envelope rules; the stream is unusable.
    class ProtocolError < StandardError; end

    # A value cannot be written as JSON: a type JSON has no form for, a number that is not
    # finite, text that is not UTF-8, or nesting without end.
    class EncodeError < TypeError; end

    # A deliberate public failure sent to the host as the structured error.
    #
    # +code+ must match the identifier syntax, +message+ is at most 4096 UTF-8 bytes, and
    # +retry_after_ms+ is an optional non-negative bound of one year.
    class RemoteError < StandardError
      attr_reader :code, :message, :retry_after_ms

      def initialize(code, message, retry_after_ms: nil)
        unless code.is_a?(String) && IDENTIFIER.match?(code)
          raise ArgumentError, "error code must match [a-z0-9][a-z0-9._/-]{0,255}"
        end
        text = message.is_a?(String) ? Plugin.utf8(message) : nil
        if text.nil? || text.bytesize > MAX_MESSAGE_BYTES
          raise ArgumentError, "error message must be UTF-8 text of at most 4096 bytes"
        end
        unless retry_after_ms.nil? || (retry_after_ms.is_a?(Integer) && retry_after_ms >= 0 && retry_after_ms <= MAX_RETRY_AFTER_MS)
          raise ArgumentError, "retry_after_ms must be an integer in [0, one year]"
        end
        super(text)
        @code = code
        @message = text
        @retry_after_ms = retry_after_ms
      end

      def to_wire
        wire = { "code" => @code, "message" => @message }
        wire["retryAfterMilliseconds"] = @retry_after_ms if @retry_after_ms && @retry_after_ms != 0
        wire
      end
    end

    module_function

    def match?(pattern, value)
      value.is_a?(String) && pattern.match?(value)
    end

    # +text+ as a valid UTF-8 String, or nil when it is not text.
    def utf8(text)
      text = text.to_s if text.is_a?(Symbol)
      return nil unless text.is_a?(String)

      case text.encoding
      when Encoding::UTF_8
        text.valid_encoding? ? text : nil
      when Encoding::BINARY, Encoding::US_ASCII
        copy = text.dup.force_encoding(Encoding::UTF_8)
        copy.valid_encoding? ? copy : nil
      else
        begin
          text.encode(Encoding::UTF_8)
        rescue EncodingError
          nil
        end
      end
    end

    # The length of +text+ in bytes of UTF-8, or -1 when it is not text.
    def utf8_length(text)
      t = utf8(text)
      t ? t.bytesize : -1
    end

    # --- strict JSON ----------------------------------------------------------

    # Decodes one frame (bytes without the LF) with the Go decoder's rules: UTF-8 only, no
    # duplicate keys, no trailing values, no non-finite constants, no unpaired surrogate
    # escapes, nesting of at most 64 levels.
    def decode_frame(raw)
      raise ProtocolError, "frame too large" if raw.bytesize > MAX_FRAME_BYTES

      text = raw.dup.force_encoding(Encoding::UTF_8)
      raise ProtocolError, "JSON must be UTF-8" unless text.valid_encoding?

      Decoder.new(text).parse
    end

    # Encodes +value+ as compact JSON, escaping everything outside printable ASCII the way
    # Python's json.dumps(ensure_ascii=True) does. Symbols are written as strings.
    def encode_frame(value)
      out = +""
      Encoder.write(value, out, 0, true)
      out.force_encoding(Encoding::BINARY)
    end

    # A deep copy of +value+ in the shape the host reads it: string keys, no symbols.
    def json_copy(value)
      decode_frame(encode_frame(value))
    rescue ProtocolError => e
      raise EncodeError, e.message
    end

    # Reads one LF-terminated frame; nil at clean EOF. Bounded by the limit.
    def read_frame(stream)
      line = stream.gets("\n", MAX_FRAME_BYTES + 2)
      return nil if line.nil? || line.empty?

      line = line.b
      unless line.end_with?("\n")
        raise ProtocolError, "frame too large" if line.bytesize > MAX_FRAME_BYTES + 1

        raise ProtocolError, "unexpected EOF inside a frame"
      end
      line.byteslice(0, line.bytesize - 1)
    end

    # The strict JSON reader behind decode_frame.
    class Decoder
      WS = /[ \t\n\r]*/.freeze
      NUMBER = /-?(?:0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?/.freeze
      PLAIN = /[^"\\\x00-\x1f]+/.freeze
      HEX4 = /[0-9a-fA-F]{4}/.freeze
      LOW = /\\u([dD][c-fC-F][0-9a-fA-F]{2})/.freeze
      SIMPLE = { '"' => '"', "\\" => "\\", "/" => "/", "b" => "\b", "f" => "\f", "n" => "\n", "r" => "\r", "t" => "\t" }.freeze

      def initialize(text)
        @s = StringScanner.new(text)
      end

      def parse
        @s.skip(WS)
        value = read_value(0)
        @s.skip(WS)
        malformed unless @s.eos?
        value
      end

      private

      def malformed
        raise ProtocolError, "malformed JSON"
      end

      def read_value(depth)
        raise ProtocolError, "JSON nesting exceeds 64 levels" if depth > MAX_DEPTH

        case @s.peek(1)
        when "{" then read_object(depth)
        when "[" then read_array(depth)
        when '"' then read_string
        when "t" then @s.skip(/true/) ? true : malformed
        when "f" then @s.skip(/false/) ? false : malformed
        when "n" then @s.skip(/null/) ? nil : malformed
        else read_number
        end
      end

      def read_number
        text = @s.scan(NUMBER) || malformed
        @s[1] || @s[2] ? Float(text) : Integer(text, 10)
      end

      def read_object(depth)
        @s.getch
        out = {}
        @s.skip(WS)
        return out if @s.skip(/\}/)

        loop do
          malformed unless @s.peek(1) == '"'
          key = read_string
          raise ProtocolError, "duplicate JSON key" if out.key?(key)

          @s.skip(WS)
          malformed unless @s.skip(/:/)
          @s.skip(WS)
          out[key] = read_value(depth + 1)
          @s.skip(WS)
          next @s.skip(WS) if @s.skip(/,/)
          return out if @s.skip(/\}/)

          malformed
        end
      end

      def read_array(depth)
        @s.getch
        out = []
        @s.skip(WS)
        return out if @s.skip(/\]/)

        loop do
          out << read_value(depth + 1)
          @s.skip(WS)
          next @s.skip(WS) if @s.skip(/,/)
          return out if @s.skip(/\]/)

          malformed
        end
      end

      def read_string
        @s.getch
        buf = +""
        loop do
          run = @s.scan(PLAIN)
          buf << run if run
          case @s.getch
          when '"'
            return buf
          when "\\"
            escape = @s.getch
            if escape == "u"
              buf << read_unicode
            else
              buf << (SIMPLE[escape] || malformed)
            end
          else
            malformed # the end of the text, or a control character
          end
        end
      end

      def read_unicode
        hex = @s.scan(HEX4) || malformed
        cp = hex.to_i(16)
        if cp >= 0xD800 && cp <= 0xDBFF
          raise ProtocolError, "unpaired surrogate escape" unless @s.scan(LOW)

          cp = 0x10000 + ((cp - 0xD800) << 10) + (@s[1].to_i(16) - 0xDC00)
        elsif cp >= 0xDC00 && cp <= 0xDFFF
          raise ProtocolError, "unpaired surrogate escape"
        end
        cp.chr(Encoding::UTF_8)
      end
    end

    # The compact JSON writer behind encode_frame.
    module Encoder
      MAX_NESTING = 1000
      ESCAPES = { '"' => '\\"', "\\" => "\\\\", "\n" => "\\n", "\r" => "\\r", "\t" => "\\t", "\b" => "\\b", "\f" => "\\f" }.freeze
      ASCII_SPECIAL = /[^\x20-\x7e]|["\\]/.freeze
      UTF8_SPECIAL = /[\x00-\x1f"\\]/.freeze

      module_function

      def write(value, out, depth, ascii)
        raise EncodeError, "nesting too deep to write as JSON" if depth > MAX_NESTING

        case value
        when nil then out << "null"
        when true then out << "true"
        when false then out << "false"
        when String, Symbol then write_string(value, out, ascii)
        when Integer then out << value.to_s
        when Float then out << float(value)
        when Hash
          out << "{"
          first = true
          value.each do |key, child|
            raise EncodeError, "object keys must be strings, not #{key.class}" unless key.is_a?(String) || key.is_a?(Symbol)

            out << "," unless first
            first = false
            write_string(key, out, ascii)
            out << ":"
            write(child, out, depth + 1, ascii)
          end
          out << "}"
        when Array
          out << "["
          value.each_with_index do |child, i|
            out << "," if i.positive?
            write(child, out, depth + 1, ascii)
          end
          out << "]"
        else
          raise EncodeError, "Object of type #{value.class} is not JSON serializable"
        end
      end

      def write_string(value, out, ascii)
        text = Plugin.utf8(value) || raise(EncodeError, "string is not UTF-8 text")
        out << '"'
        if ascii
          out << text.gsub(ASCII_SPECIAL) { |c| ESCAPES[c] || unicode(c.ord) }
        else
          out << text.gsub(UTF8_SPECIAL) { |c| ESCAPES[c] || format("\\u%04x", c.ord) }
        end
        out << '"'
      end

      def unicode(cp)
        return format("\\u%04x", cp) if cp < 0x10000

        cp -= 0x10000
        format("\\u%04x\\u%04x", 0xD800 + (cp >> 10), 0xDC00 + (cp & 0x3FF))
      end

      # A float as Python's repr writes it: the shortest digits that read back, in positional
      # form unless the exponent is below -4 or above 15.
      def float(value)
        raise EncodeError, "Out of range float values are not JSON compliant" unless value.finite?
        return value.to_s if value.zero? # "0.0" or "-0.0"

        mantissa, exponent = value.abs.to_s.split("e")
        whole, fraction = mantissa.split(".")
        digits = whole + fraction.to_s
        point = whole.length + exponent.to_i # value = 0.digits * 10**point
        stripped = digits.sub(/\A0+/, "")
        point -= digits.length - stripped.length
        digits = stripped.sub(/0+\z/, "")
        sign = value.negative? ? "-" : ""
        if point <= -4 || point > 16
          exp = point - 1
          body = digits.length > 1 ? "#{digits[0]}.#{digits[1..]}" : digits
          format("%s%se%s%02d", sign, body, exp.negative? ? "-" : "+", exp.abs)
        elsif point <= 0
          "#{sign}0.#{"0" * -point}#{digits}"
        elsif point >= digits.length
          "#{sign}#{digits}#{"0" * (point - digits.length)}.0"
        else
          "#{sign}#{digits[0, point]}.#{digits[point..]}"
        end
      end
    end

    # --- time -----------------------------------------------------------------

    ZERO_TIME = -62_135_596_800

    # RFC 3339 to a Time; nil when invalid or the zero time (Go's "no deadline given").
    def parse_deadline(value)
      return nil unless value.is_a?(String)

      m = TIME.match(value)
      return nil unless m

      year, month, day, hour, minute, second = (1..6).map { |i| m[i].to_i }
      fraction = m[7]
      micro = fraction ? (fraction[1..] + "000000")[0, 6].to_i : 0
      zone = m[8]
      offset = 0
      if zone != "Z" && zone != "z"
        offset = (zone[1, 2].to_i * 60) + zone[4, 2].to_i
        offset = -offset if zone[0] == "-"
      end
      return nil if year < 1 || month < 1 || month > 12 || day < 1 || day > days_in_month(year, month)
      return nil if hour > 23 || minute > 59 || second > 59 || offset.abs >= 24 * 60

      seconds = (days_from_civil(year, month, day) * 86_400) + (hour * 3600) + (minute * 60) + second - (offset * 60)
      return nil if seconds == ZERO_TIME && micro.zero?

      Time.at(seconds, micro, :usec).utc
    end

    def days_in_month(year, month)
      return 29 if month == 2 && (year % 4).zero? && ((year % 100).nonzero? || (year % 400).zero?)

      [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1]
    end

    # Days since 1970-01-01 in the proleptic Gregorian calendar.
    def days_from_civil(year, month, day)
      year -= 1 if month <= 2
      era = year.div(400)
      yoe = year - (era * 400)
      doy = ((153 * (month + (month > 2 ? -3 : 9))) + 2) / 5 + day - 1
      doe = (yoe * 365) + (yoe / 4) - (yoe / 100) + doy
      (era * 146_097) + doe - 719_468
    end

    # The instant at millisecond precision, rounded down, so it is never after the call's own.
    def format_time(time)
      time.utc.strftime("%Y-%m-%dT%H:%M:%S.%LZ")
    end
  end
end
