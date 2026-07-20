_dockstar() {
  local commands="status devices allow deny trust mode apply diagnose wake config completion version"
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "$commands" -- "${COMP_WORDS[COMP_CWORD]}") )
  fi
}
complete -F _dockstar dockstar
