const child_process = require('child_process')

exports.handler = (event, context) => {
    process.env.PATH = `${process.env.PATH}:${process.env.LAMBDA_TASK_ROOT}/drain_container_instance/bin`
    const proc = child_process.spawn('drain-container-instance', [ JSON.stringify(event) ], { stdio: 'inherit' })

    proc.on('close', code => {
        if(code !== 0) {
            return context.done(new Error("Process exited with non-zero status code"))
        }
        context.done(null)
    })
}
