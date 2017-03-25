# require 'pry'
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

def calculate_interface_offset(text, func, line_offset)
  match_data = text.match("#{func}")

  if match_data
    offset_data = match_data.offset(0)
  end

  # Add one to offset the space
  offset_data[0] += line_offset + 1
  offset_data[1] += line_offset

  offset_data
end

def calculate_function_offset(text, func, line_offset)
  match_data = text.match("\s#{func}")

  if match_data
    offset_data = match_data.offset(0)
  end

  # Add one to offset the space
  offset_data[0] += line_offset + 1
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

def find_incoming_context(line)
  # Example:
  #   "func (user *Model) UpdateLegacy(ctx context.Context, serviceID string) error {"
  #   "func UpdateLegacy(parent context.Context, serviceID string) error {"
  line[/ \w+\((.*) context.Context,/, 1]
end

def find_child_context(line)
  # Example:
  #   "childCtx := tracer.InsertSpanIntoContext(ctx, span)"
  #   "ctx, cancel = context.WithTimeout(context.Background(), timeout)"
  line[/( .*[c|C]tx).*[=|:=]/, 1]
end

def insert_context(line, func, context)
  # If this line is a normal function call
  if line.match("#{func}\\(")
    line = line.gsub("#{func}(", "#{func}(#{context.strip}, ")
  # If this line is a function reassignment
  elsif line.match("#{func} = func\\(")
    line = line.gsub("#{func} = func(", "#{func} = func(ctx context.Context, ")
  end

  # TODO: This can be optimized
  # If no parameter follows the context parameter, remove the comma and blank space
  line.gsub(", )", ")")
end

def update_line_with_context(line, func, child_context, incoming_context)
  if child_context
    # If a child context is found, propagate the child context
    insert_context(line, func, child_context)
  elsif incoming_context
    # If incoming context is found, propagate it
    insert_context(line, func, incoming_context)
  else
    # If no earlier context is found, create a new one
    insert_context(line, func, "context.Background()")
  end
end

def replace_line(filename, func, line_num)
  temp_file = Tempfile.new('temp_file')
  num = 0
  scope = ""
  scope_line_num = 0
  incoming_context = nil
  child_context = nil

  File.open(filename, 'r') do |file|
    file.each_line do |line|

      # Only process if it has yet to reach to the relevant line
      if num < line_num
        # Entering a new function scope
        if line =~ (/func .* {|var .* func\(/)
          scope = line
          scope_line_num = num

          incoming_context = find_incoming_context(line)

          # Entering a new function scope, previous child_contexts doesn't count
          child_context = nil
        end

        # Only find child_context if it is not already found
        child_context ||= find_child_context(line)
      end

      # We have reached the line that needs to be replaced
      if num == line_num
        new_line = update_line_with_context(line, func, child_context, incoming_context)

        # Remove { for pretty printing}
        puts "Do you want to make the following changes inside "
        puts "#{filename}: #{scope_line_num}:#{scope.chomp!}? (y/n)"
        puts "#{line_num}: #{line}"
        puts "  to"
        puts "#{line_num}: #{new_line}"
        print "> (y/n) : "
        guru_confirmation = $stdin.gets.chomp
        puts

        if guru_confirmation == 'y'
          line = new_line
        else
          puts "skipping to the next result"
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

# Replace the function itself
def replace_function(filename, func, line_num, name_type)
  temp_file = Tempfile.new('temp_file')
  num = 0

  File.open(filename, 'r') do |file|
    file.each_line do |line|
      if num == line_num
        line = insert_context(line, func, "ctx context.Context")
        temp_file.puts line

        if name_type == 'function'
          puts "Create span in #{func}?"
          print "> (y/n) : "
          span_confirmation = $stdin.gets.chomp
          puts

          if span_confirmation == 'y'
            temp_file.puts "\tspan, _ := tracer.CreateSpanFromContext(ctx, logTag+\".#{func}\")\n\tdefer span.Finish()\n"
           end
        end
      else
        temp_file.puts line
      end

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
  name = ARGV[1]
  name_type = ARGV[2]

  if ARGV.size != 3
    puts "usage: ruby context_parser.rb <full_path_filename> <name> <name_type>"
  end

  grep_str = ""
  if name_type == 'function'
    grep_str = "\"func .*#{name}(\" -e \"var #{name} = func(\""
  elsif name_type == 'interface'
    grep_str = "\"^\\s#{name}(\""
  else
    puts "name_type not supported"
    exit
  end

  #grep -a -b -n "GetID" user.go
  grep = `grep -ban -e #{grep_str} #{filename}`
  grep_results = parse_grep_result(grep)
  impacted_files = []
  # need to ask user to choose the right function
  grep_results.each do |result|
    puts "Is this the correct #{name_type}? (y/n)"
    puts "  #{result[:line]} => #{result[:text]}"
    print "> (y/n) : "
    grep_confirmation = $stdin.gets.chomp
    puts

    if grep_confirmation != 'y'
      puts "skipping to the next function"
      next
    end

    if name_type == 'function'
      offset = calculate_function_offset(result[:text], name, result[:line_offset])
    elsif name_type == 'interface'
      offset = calculate_interface_offset(result[:text], name, result[:line_offset])
    else
      puts "name_type not supported"
      exit
    end

    #guru referrers user.go:#2072,#2080
    guru = `guru referrers #{filename}:##{offset[0]},##{offset[1]}`
    guru_results = parse_guru_result(guru)

    impacted_files << guru_results.collect {|result| result[:path]}

    # This is the function itself
    first_guru_result = guru_results[0]

    # Remove the first element cause it needs to be processed differently
    guru_results.shift

    # Check if first, do not find function name if it is
    guru_results.each do |guru_result|
      replace_line(guru_result[:path], name, guru_result[:start_data][:line] - 1)
    end

    replace_function(first_guru_result[:path], name, first_guru_result[:start_data][:line] - 1, name_type)
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
