require 'pry'
require 'tempfile'
require 'fileutils'

def parse_grep_result(grep_result)
  grep_lines = grep_result.split("\n")

  data = []
  grep_lines.each_with_index do |line, idx|
    line_num, line_offset, text = line.split(":")
    data[idx] = {
        :line => line_num,
        :line_offset => line_offset.to_i,
        :text => text
    }
  end

  data
end

def calculate_offset(text, func, line_offset)
  match_data = text.match(func)

  if match_data
    offset_data = match_data.offset(0)
  end

  offset_data[0] += line_offset
  offset_data[1] += line_offset

  offset_data
end

def parse_guru_result(guru)
  guru_lines = guru.split("\n")

  data = []
  guru_lines.each_with_index do |line, idx|
    path, location, msg = line.split(/:(?!=)/)
    start_loc, end_loc = location.split("-")
    start_line, start_offset = start_loc.split(".")
    end_line, end_offset = end_loc.split(".")

    data[idx] = {
      :path => path,
      :location => location,
      :text => msg,
      :start_data => {
        :loc => start_loc.to_i,
        :line => start_line.to_i,
        :offset =>start_offset.to_i
      },
      :end_data => {
        :loc => end_loc.to_i,
        :line => end_line.to_i,
        :offset => end_offset.to_i
      }
    }
  end

  data
end

def replace_line(filename, func, line_num, offset_start, offset_end, not_first)
  temp_file = Tempfile.new('foo')
  num = 0

  function = ""
  File.open(filename, 'r') do |file|
    file.each_line do |line|

      if num < line_num && line.match(/func .* {|var .* func\(/)
        function = line
      end

      # Only add context.Context() if it doesn't already exist
      if num == line_num
        # Remove { for pretty printing}
        puts "Usage inside function #{function.gsub("{", "")} updated\n" if not_first

        if line !~ /context.Background()/
          line.gsub!("#{func}(", "#{func}(context.Background(), ")
        end
      end
      temp_file.puts line
      num += 1
    end
  end
  temp_file.close
  FileUtils.mv(temp_file.path, filename)
ensure
  temp_file.close
  temp_file.unlink
end

def main
  # filename = "/Users/ryanlaw/go/src/github.com/myteksi/go/grab-id/models/user/user.go"
  # func = "LoadByID"

  filename = ARGV[0]
  func = ARGV[1]

  if ARGV.size != 2
    puts "usage: ruby context_parser.rb <full_path_filename> <function_name>"
  end

  #grep -a -b -n "GetID" user.go
  grep = `grep -ban "func .* #{func}" #{filename}`
  grep_results = parse_grep_result(grep)

  # need to ask user to choose the right function
  grep_results.each do |grep_result|
    puts "Is this the correct function? (y/n)"
    puts "  #{grep_result[:line]} => #{grep_result[:text]}"
    print "> (y/n) : "
    grep_confirmation = $stdin.gets.chomp
    puts

    if grep_confirmation != 'y'
      puts "skipping to the next function"
      next
    end

    result = grep_result
    offset = calculate_offset(result[:text], func, result[:line_offset])

    #guru referrers user.go:#2072,#2080
    guru = `guru referrers #{filename}:##{offset[0]},##{offset[1]}`
    guru_results = parse_guru_result(guru)

    # Check if first, do not find function name if it is
    self_processed = false
    guru_results.each do |guru_result|
      puts "Do you want to update the following line with context? (y/n)"
      puts "  #{guru_result[:path]}:#{guru_result[:location]} => #{guru_result[:text]}"
      print "> (y/n) : "
      guru_confirmation = $stdin.gets.chomp
      puts

      if guru_confirmation != 'y'
        puts "skipping to the next result"
        next
      end

      replace_line(guru_result[:path],
                   func,
                   (guru_result[:start_data][:line].to_i) - 1,
                   guru_result[:start_data][:offset],
                   guru_result[:end_data][:offset],
                   self_processed)

      self_processed = true
    end
  end

  puts "We are done!"
end

main
