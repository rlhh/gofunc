# require 'pry'
require 'tempfile'
require 'fileutils'

def main
  fullpath = ARGV[0]

  if ARGV.size != 1
    puts "usage: ruby context_parser_http.rb <full_director_path>"
  end

  impacted_files = []

  Dir.foreach(fullpath) do |filename|
    next if filename == '.' or filename == '..'

    full_file_path = fullpath+'/'+filename
    File.open(full_file_path, 'r') do |file|
      temp_file = Tempfile.new('temp_file')

      file.each_line do |line|
        temp_file.puts line

        # if not function declaration line
        if !(line =~ /func /)
          next
        end

        # Get function name and name of http.Request parameter
        match_data = line.match(/func (\w*).* (\w*) \*http.Request/)

        # Skip functions without http.Request parameter
        # functions with more than 3 matches are ambiguous
        if match_data && match_data.length == 3
          func = match_data[1]
          http_req = match_data[2]

          temp_file.puts "\tspan, _ := tracer.CreateSpanFromContext(#{http_req}.Context(), logTag+\".#{func}\")\n"
          temp_file.puts "\tdefer span.Finish()\n"

          impacted_files << full_file_path
        end
      end

      temp_file.close
      FileUtils.mv(temp_file.path, full_file_path)
      end
  end

  # gofmt impacted files 
  impacted_files.flatten.uniq.each do |file_path|
    `gofmt -s -w #{file_path}`
    `goimports -w #{file_path}`
    # -i '' is required when running on mac because it uses the BSD version of sed
    `sed -i '' 's/\\"context\\"/\\"golang.org\\/x\\/net\\/context\\"/' #{file_path}`
    # perform another import to fix the arrangement of the imports
    `goimports -w #{file_path}`
  end
  puts "We are done!"
end

main
