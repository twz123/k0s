def define_env(env):
    @env.filter
    def ljust(value, width):
        return str(value).ljust(width)
